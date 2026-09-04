import { apiClient } from "../http/index.js";
import type { SessionRefresher } from "../http/types.js";
import { AuthMachine, authMachine } from "./auth-machine.js";
import type { AuthState } from "./types.js";

// auth-internals.md §4: the access token's lifetime is a fixed
// system-wide constant (900s), only ever returned to the frontend in a
// login/refresh response body — unavailable from GET /auth/me, so the
// mount-time session check schedules from this default instead.
const DEFAULT_LIFETIME_SECONDS = 900;
const REFRESH_AT_FRACTION = 0.8;

export class TokenRefreshScheduler {
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    private readonly machine: AuthMachine,
    private readonly refresher: SessionRefresher,
  ) {}

  schedule(expiresInSeconds: number): void {
    this.clear();
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.runRefresh();
    }, expiresInSeconds * REFRESH_AT_FRACTION * 1000);
  }

  clear(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }

  private async runRefresh(): Promise<void> {
    // No-ops if something else (logout, an api-client.ts session_expired)
    // already moved the machine away from "authenticated" before this
    // timer fired.
    if (!this.machine.transition({ type: "refresh_started" })) return;

    // Goes through the shared APIClient's own coalescing (not an
    // independent fetch) — a proactive refresh racing a reactive
    // 401-triggered one would otherwise hit auth-internals.md §4's
    // rotation lock, and the loser gets back a bare 401.
    const result = await this.refresher.refreshSession();
    if (!result.ok) {
      this.machine.transition({ type: "refresh_failed" });
      return;
    }

    // Only reschedule if the transition actually applied — logout or an
    // external session_expired can race this same window (both are valid
    // from "refreshing"), and rescheduling after either would leave a
    // stray timer outliving the session.
    if (this.machine.transition({ type: "refresh_succeeded" })) {
      this.schedule(result.expiresIn ?? DEFAULT_LIFETIME_SECONDS);
    }
  }
}

// Starts/clears the scheduler in reaction to the machine's own state,
// rather than at specific call sites — session_expired can fire from
// http/api-client.ts's unrecoverable-401 path, entirely outside
// auth-provider.tsx, so nothing short of watching the machine itself
// reliably catches every path in and out of "authenticated".
export function wireAutoRefresh(machine: AuthMachine, refresher: SessionRefresher): TokenRefreshScheduler {
  const scheduler = new TokenRefreshScheduler(machine, refresher);
  let previousStatus: AuthState["status"] = machine.getState().status;

  machine.subscribe(() => {
    const { status } = machine.getState();
    const wasAuthenticated = previousStatus === "authenticated";
    const isAuthenticated = status === "authenticated";

    // previousStatus !== "refreshing" excludes the refresh_succeeded
    // transition — runRefresh() above already reschedules itself with
    // the real expires_in it just received, so scheduling again here
    // with the default would double-schedule.
    if (isAuthenticated && !wasAuthenticated && previousStatus !== "refreshing") {
      scheduler.schedule(DEFAULT_LIFETIME_SECONDS);
    } else if (!isAuthenticated && wasAuthenticated) {
      scheduler.clear();
    }
    previousStatus = status;
  });

  return scheduler;
}

// The browser shell's shared scheduler, wired to the browser apiClient's
// own refresh coalescing. A non-browser consumer (none exists in this
// repo yet) calls wireAutoRefresh with its own AuthMachine/FetchAPIClient
// instead of using this one — the same pattern apiClient itself follows.
export const tokenRefreshScheduler = wireAutoRefresh(authMachine, apiClient);
