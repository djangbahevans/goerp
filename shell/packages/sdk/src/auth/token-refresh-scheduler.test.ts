import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RefreshOutcome, SessionRefresher } from "../http/types.js";
import { AuthMachine } from "./auth-machine.js";
import { TokenRefreshScheduler, wireAutoRefresh } from "./token-refresh-scheduler.js";
import type { CurrentTenant, CurrentUser } from "./types.js";

const user: CurrentUser = { id: "u1", email: "a@example.com", roles: [], amr: ["pwd"], mfaVerifiedAt: null };
const tenant: CurrentTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

function fakeRefresher(fn: () => Promise<RefreshOutcome>): SessionRefresher {
  return { refreshSession: vi.fn(fn) };
}

function authenticatedMachine(): AuthMachine {
  const machine = new AuthMachine();
  machine.transition({ type: "check_session" });
  machine.transition({ type: "session_checked", user, tenant });
  return machine;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("TokenRefreshScheduler", () => {
  it("schedules the refresh at exactly 80% of the given lifetime", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);

    scheduler.schedule(900);

    await vi.advanceTimersByTimeAsync(719_999);
    expect(refresher.refreshSession).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
  });

  it("reschedules using the new response's expires_in, not the original value", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 600 }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);

    // Rescheduled at 600 * 0.8 = 480s, not another 720s.
    await vi.advanceTimersByTimeAsync(480_000 - 1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(2);
  });

  it("transitions to unauthenticated on a failed refresh and does not reschedule", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: false }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);

    expect(machine.getState()).toEqual({ status: "unauthenticated" });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1); // no further attempts scheduled
  });

  it("clear() cancels a pending refresh", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);

    scheduler.schedule(900);
    scheduler.clear();
    await vi.advanceTimersByTimeAsync(10_000_000);

    expect(refresher.refreshSession).not.toHaveBeenCalled();
  });

  it("does nothing if the machine already left authenticated before the timer fired", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);

    scheduler.schedule(900);
    machine.transition({ type: "logout_started" });
    machine.transition({ type: "logout_complete" });

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);

    expect(refresher.refreshSession).not.toHaveBeenCalled();
  });

  it("does not reschedule if logout races a successful refresh", async () => {
    // The refresh call resolves ok, but the machine moved to logging_out
    // while it was in flight — refresh_succeeded (only valid from
    // "refreshing") must no-op, and no new timer should be armed.
    const machine = authenticatedMachine();
    let resolveRefresh!: (outcome: RefreshOutcome) => void;
    const refreshSession = vi.fn(
      () => new Promise<RefreshOutcome>((resolve) => (resolveRefresh = resolve)),
    );
    const scheduler = new TokenRefreshScheduler(machine, { refreshSession });

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(refreshSession).toHaveBeenCalledTimes(1);

    machine.transition({ type: "logout_started" });
    resolveRefresh({ ok: true, expiresIn: 900 });
    await vi.advanceTimersByTimeAsync(0);
    machine.transition({ type: "logout_complete" });

    // If a stray timer had been armed by the raced refresh_succeeded, it
    // would have fired well within this window.
    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
});

describe("wireAutoRefresh", () => {
  it("starts the scheduler when the machine enters authenticated via login", async () => {
    const machine = new AuthMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    wireAutoRefresh(machine, refresher);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
  });

  it("clears the scheduler on logout so no stray timer outlives the session", async () => {
    const machine = new AuthMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    wireAutoRefresh(machine, refresher);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    machine.transition({ type: "logout_started" });
    machine.transition({ type: "logout_complete" });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refresher.refreshSession).not.toHaveBeenCalled();
  });

  it("clears the scheduler on an external session_expired (e.g. from the API client)", async () => {
    const machine = new AuthMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    wireAutoRefresh(machine, refresher);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    machine.transition({ type: "session_expired" });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refresher.refreshSession).not.toHaveBeenCalled();
  });

  it("does not double-schedule across a successful refresh cycle", async () => {
    const machine = new AuthMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    wireAutoRefresh(machine, refresher);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000 - 1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(2);
  });
});
