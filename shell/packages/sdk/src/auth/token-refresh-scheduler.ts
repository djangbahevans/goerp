import { apiClient } from "../http/index.js";
import type { SessionRefresher } from "../http/types.js";
import { type AuthMachine, authMachine } from "./auth-machine.js";
import { sessionActivity } from "./session-activity.js";
import type { AuthState } from "./types.js";

// GET /auth/me does not return expires_in, so a mount-time check uses the access-token lifetime.
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
    this.timer = setTimeout(
      () => {
        this.timer = null;
        if (sessionActivity.hasActivity()) void this.runRefresh();
      },
      expiresInSeconds * REFRESH_AT_FRACTION * 1000,
    );
  }

  clear(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }

  private async runRefresh(): Promise<void> {
    if (!this.machine.transition({ type: "refresh_started" })) return;
    // The refresher owns acknowledgment so coalesced callers retain input received during its request.
    const result = await this.refresher.refreshSession();
    if (!result.ok) {
      this.machine.transition({ type: "refresh_failed" });
      return;
    }

    // Logout can invalidate the session while its refresh is in flight.
    if (this.machine.transition({ type: "refresh_succeeded" })) {
      this.schedule(result.expiresIn ?? DEFAULT_LIFETIME_SECONDS);
    }
  }
}

export function wireAutoRefresh(machine: AuthMachine, refresher: SessionRefresher): TokenRefreshScheduler {
  const scheduler = new TokenRefreshScheduler(machine, refresher);
  let previousStatus: AuthState["status"] = machine.getState().status;

  refresher.subscribeRefresh?.((expiresIn) => {
    const { status } = machine.getState();
    if (status === "authenticated" || status === "refreshing") scheduler.schedule(expiresIn);
  });

  machine.subscribe(() => {
    const { status } = machine.getState();
    if (status === "authenticated" && previousStatus !== "authenticated" && previousStatus !== "refreshing") {
      sessionActivity.acknowledge();
      sessionActivity.start();
      scheduler.schedule(DEFAULT_LIFETIME_SECONDS);
    } else if (status !== "authenticated" && status !== "refreshing") {
      scheduler.clear();
      sessionActivity.stop();
    }
    previousStatus = status;
  });

  return scheduler;
}

export const tokenRefreshScheduler = wireAutoRefresh(authMachine, apiClient);
