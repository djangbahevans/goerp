import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FetchAPIClient } from "../http/api-client.js";
import type { RefreshOutcome, SessionRefresher } from "../http/types.js";
import { AuthMachine } from "./auth-machine.js";
import { sessionActivity } from "./session-activity.js";
import { TokenRefreshScheduler, wireAutoRefresh } from "./token-refresh-scheduler.js";
import type { CurrentTenant, CurrentUser } from "./types.js";

const user: CurrentUser = {
  id: "u1",
  email: "a@example.com",
  name: null,
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  passwordChangeRequired: false,
  passwordMinLength: 12,
  phone: null,
  title: null,
  theme: "system" as const,
  contrast: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant: CurrentTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  firstDayOfWeek: "monday" as const,
  numberFormat: "1,234.56" as const,
  passwordMinLength: 12,
};

function fakeRefresher(fn: () => Promise<RefreshOutcome>): SessionRefresher {
  return {
    refreshSession: vi.fn(async () => {
      const activity = sessionActivity.snapshot();
      const result = await fn();
      if (result.ok) sessionActivity.acknowledge(activity);
      return result;
    }),
  };
}

function authenticatedMachine(): AuthMachine {
  const machine = new AuthMachine();
  machine.transition({ type: "check_session" });
  machine.transition({ type: "session_checked", user, tenant });
  return machine;
}

beforeEach(() => {
  vi.useFakeTimers();
  sessionActivity.acknowledge();
  sessionActivity.record();
});

afterEach(() => {
  sessionActivity.stop();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("TokenRefreshScheduler", () => {
  it("preserves input while a proactive refresh joins an in-flight reactive refresh", async () => {
    const machine = authenticatedMachine();
    const client = new FetchAPIClient();
    const scheduler = new TokenRefreshScheduler(machine, client);
    let finishRefresh!: (response: Response) => void;
    let attempts = 0;
    const fetchMock = vi.fn((path: string) => {
      if (path === "/auth/refresh")
        return new Promise<Response>((resolve) => {
          finishRefresh = resolve;
        });
      attempts += 1;
      return Promise.resolve(
        new Response(JSON.stringify(attempts === 1 ? { error: { code: "unauthenticated" } } : []), {
          status: attempts === 1 ? 401 : 200,
        }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);
    const request = client.get("/contacts");
    await vi.advanceTimersByTimeAsync(0);
    sessionActivity.record();
    scheduler.schedule(1);
    await vi.advanceTimersByTimeAsync(800);
    finishRefresh(new Response(JSON.stringify({ expires_in: 900 })));
    await request;
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock.mock.calls.filter(([path]) => path === "/auth/refresh")).toHaveLength(1);
    expect(sessionActivity.hasActivity()).toBe(true);
    expect(machine.getState().status).toBe("authenticated");
    scheduler.clear();
  });

  it("lets an idle token lapse without refreshing or rescheduling", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);
    sessionActivity.acknowledge();
    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refresher.refreshSession).not.toHaveBeenCalled();
    expect(machine.getState().status).toBe("authenticated");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("requires new input for each proactive refresh cycle", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);
    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(720_000);
    await vi.advanceTimersByTimeAsync(720_000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
  });

  it("does not renew a hidden document even when input is pending", async () => {
    vi.stubGlobal("document", { visibilityState: "hidden" });
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    const scheduler = new TokenRefreshScheduler(authenticatedMachine(), refresher);
    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(720_000);
    expect(refresher.refreshSession).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

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

    sessionActivity.record();
    await vi.advanceTimersByTimeAsync(480_000 - 1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(2);
  });

  it("ends the session as expired on a failed refresh and does not reschedule", async () => {
    const machine = authenticatedMachine();
    const refresher = fakeRefresher(async () => ({ ok: false }));
    const scheduler = new TokenRefreshScheduler(machine, refresher);

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);

    expect(machine.getState()).toEqual({ status: "unauthenticated", sessionExpired: true, user, tenant });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
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
    const machine = authenticatedMachine();
    let resolveRefresh!: (outcome: RefreshOutcome) => void;
    const refreshSession = vi.fn(() => new Promise<RefreshOutcome>((resolve) => (resolveRefresh = resolve)));
    const scheduler = new TokenRefreshScheduler(machine, { refreshSession });

    scheduler.schedule(900);
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(refreshSession).toHaveBeenCalledTimes(1);

    machine.transition({ type: "logout_started" });
    resolveRefresh({ ok: true, expiresIn: 900 });
    await vi.advanceTimersByTimeAsync(0);
    machine.transition({ type: "logout_complete" });

    await vi.advanceTimersByTimeAsync(10_000_000);
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
});

describe("wireAutoRefresh", () => {
  it("restarts after a reactive refresh of a token whose proactive timer stopped", async () => {
    const machine = new AuthMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 600 }));
    let notify!: (expiresIn: number) => void;
    refresher.subscribeRefresh = (listener) => {
      notify = listener;
      return () => {};
    };
    wireAutoRefresh(machine, refresher);
    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });
    await vi.advanceTimersByTimeAsync(900_000);
    expect(refresher.refreshSession).not.toHaveBeenCalled();

    notify(600);
    sessionActivity.record();
    await vi.advanceTimersByTimeAsync(479_999);
    expect(refresher.refreshSession).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(refresher.refreshSession).toHaveBeenCalledOnce();
  });

  it("starts the scheduler when the machine enters authenticated via login", async () => {
    const machine = new AuthMachine();
    const refresher = fakeRefresher(async () => ({ ok: true, expiresIn: 900 }));
    wireAutoRefresh(machine, refresher);

    machine.transition({ type: "check_session" });
    machine.transition({ type: "session_checked", user, tenant });

    sessionActivity.record();
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

    sessionActivity.record();
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);

    sessionActivity.record();
    await vi.advanceTimersByTimeAsync(900 * 0.8 * 1000 - 1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(refresher.refreshSession).toHaveBeenCalledTimes(2);
  });
});
