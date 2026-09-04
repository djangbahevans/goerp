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

  constructor(private readonly machine: AuthMachine) {}

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

    try {
      const response = await fetch("/auth/refresh", { method: "POST", credentials: "include" });
      if (!response.ok) {
        this.machine.transition({ type: "refresh_failed" });
        return;
      }
      const body = (await response.json()) as { expires_in: number };
      this.machine.transition({ type: "refresh_succeeded" });
      this.schedule(body.expires_in);
    } catch {
      this.machine.transition({ type: "refresh_failed" });
    }
  }
}

// Starts/clears the scheduler in reaction to the machine's own state,
// rather than at specific call sites — session_expired can fire from
// http/api-client.ts's unrecoverable-401 path, entirely outside
// auth-provider.tsx, so nothing short of watching the machine itself
// reliably catches every path in and out of "authenticated".
export function wireAutoRefresh(machine: AuthMachine): TokenRefreshScheduler {
  const scheduler = new TokenRefreshScheduler(machine);
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

export const tokenRefreshScheduler = wireAutoRefresh(authMachine);
