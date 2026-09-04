import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthMachine } from "./auth-machine.js";
import { TokenRefreshScheduler, wireAutoRefresh } from "./token-refresh-scheduler.js";
import type { CurrentTenant, CurrentUser } from "./types.js";

const user: CurrentUser = { id: "u1", email: "a@example.com", roles: [], amr: ["pwd"], mfaVerifiedAt: null };
const tenant: CurrentTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
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
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("TokenRefreshScheduler", () => {
  it("schedules the refresh at exactly 80% of the given lifetime", async () => {
    const machine = authenticatedMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    const scheduler = new TokenRefreshScheduler(machine);

    scheduler.schedule(900);

    await vi.advanceTimersByTimeAsync(719_999);
    expect(fetchMock).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledWith("/auth/refresh", { method: "POST", credentials: "include" });
  });

  it("reschedules using the new response's expires_in, not the original value", async () => {
    const machine = authenticatedMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 600 }));
    vi.stubGlobal("fetch", fetchMock);
    const scheduler = new TokenRefreshScheduler(machine);

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Rescheduled at 600 * 0.8 = 480s, not another 720s.
    await vi.advanceTimersByTimeAsync(480_000 - 1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("transitions to unauthenticated on a failed refresh and does not reschedule", async () => {
    const machine = authenticatedMachine();
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse(401, { error: { code: "invalid_refresh_token" } })));
    const scheduler = new TokenRefreshScheduler(machine);

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);

    expect(machine.getState()).toEqual({ status: "unauthenticated" });

    const fetchMock = vi.mocked(fetch);
    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(fetchMock).toHaveBeenCalledTimes(1); // no further attempts scheduled
  });

  it("transitions to unauthenticated when the refresh call itself throws", async () => {
    const machine = authenticatedMachine();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("network down");
      }),
    );
    const scheduler = new TokenRefreshScheduler(machine);

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);

    expect(machine.getState()).toEqual({ status: "unauthenticated" });
  });

  it("clear() cancels a pending refresh", async () => {
    const machine = authenticatedMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    const scheduler = new TokenRefreshScheduler(machine);

    scheduler.schedule(900);
    scheduler.clear();
    await vi.advanceTimersByTimeAsync(10_000_000);

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("does nothing if the machine already left authenticated before the timer fired", async () => {
    const machine = authenticatedMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    const scheduler = new TokenRefreshScheduler(machine);

    scheduler.schedule(900);
    machine.transition({ type: "logout_started" });
    machine.transition({ type: "logout_complete" });

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);

    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("wireAutoRefresh", () => {
  it("starts the scheduler when the machine enters authenticated via login", async () => {
    const machine = new AuthMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    wireAutoRefresh(machine);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("clears the scheduler on logout so no stray timer outlives the session", async () => {
    const machine = new AuthMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    wireAutoRefresh(machine);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    machine.transition({ type: "logout_started" });
    machine.transition({ type: "logout_complete" });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("clears the scheduler on an external session_expired (e.g. from the API client)", async () => {
    const machine = new AuthMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    wireAutoRefresh(machine);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    machine.transition({ type: "session_expired" });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("does not double-schedule across a successful refresh cycle", async () => {
    const machine = new AuthMachine();
    const fetchMock = vi.fn(async () => jsonResponse(200, { expires_in: 900 }));
    vi.stubGlobal("fetch", fetchMock);
    wireAutoRefresh(machine);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });

    // First refresh at 720s, second at 720s + 720s (each cycle reschedules
    // at 80% of the same 900s the mock always returns).
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000 - 1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
