import type { AuthEvent, AuthState } from "./types.js";

// The transition table shell-architecture.md §7 documents. Any event that
// doesn't apply to the machine's current status is a no-op (returns the
// same state reference) rather than an error — callers outside React
// (the global 401 handler, the token-refresh scheduler) fire events
// without knowing what state the machine happens to be in right now.
export function authTransition(state: AuthState, event: AuthEvent): AuthState {
  switch (event.type) {
    case "check_session":
      return state.status === "idle" ? { status: "checking" } : state;

    case "session_checked":
      return state.status === "checking" ? { status: "authenticated", user: event.user, tenant: event.tenant } : state;

    case "session_check_failed":
      return state.status === "checking" ? { status: "unauthenticated" } : state;

    case "login_started":
      return state.status === "unauthenticated" ? { status: "checking" } : state;

    case "login_succeeded":
      return state.status === "checking" ? { status: "authenticated", user: event.user, tenant: event.tenant } : state;

    case "login_requires_mfa":
      return state.status === "checking"
        ? { status: "mfa_required", challengeToken: event.challengeToken, methods: event.methods }
        : state;

    case "login_failed":
      return state.status === "checking" ? { status: "unauthenticated" } : state;

    case "mfa_verified":
      return state.status === "mfa_required"
        ? { status: "authenticated", user: event.user, tenant: event.tenant }
        : state;

    case "mfa_failed":
      // auth-internals.md §8 step 4 consumes the mfa_token on this
      // attempt whether or not the code was correct — the challenge
      // can't be retried, so only a fresh login (a fresh mfa_token) can
      // recover from here.
      return state.status === "mfa_required" ? { status: "unauthenticated" } : state;

    case "refresh_started":
      return state.status === "authenticated"
        ? { status: "refreshing", user: state.user, tenant: state.tenant }
        : state;

    case "refresh_succeeded":
      return state.status === "refreshing"
        ? { status: "authenticated", user: state.user, tenant: state.tenant }
        : state;

    case "refresh_failed":
      return state.status === "refreshing" ? { status: "unauthenticated" } : state;

    case "session_expired":
      return state.status === "authenticated" || state.status === "refreshing" ? { status: "unauthenticated" } : state;

    case "logout_started":
      return state.status === "authenticated" || state.status === "refreshing" ? { status: "logging_out" } : state;

    case "logout_complete":
      return state.status === "logging_out" ? { status: "unauthenticated" } : state;
  }
}

type Listener = () => void;

// AuthMachine is the single source of truth for auth state, held outside
// React so code with no component tree of its own — a global fetch
// wrapper's 401 handler, a setTimeout-based token-refresh scheduler — can
// drive it directly. AuthProvider (auth-provider.tsx) subscribes to the
// exported `authMachine` singleton via useSyncExternalStore rather than
// owning the state itself.
export class AuthMachine {
  private state: AuthState = { status: "idle" };
  private readonly listeners = new Set<Listener>();

  getState = (): AuthState => this.state;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  // Returns whether event actually changed state. A caller uses this to
  // detect a rejected "start" transition — e.g. login_started fired while
  // a mount-time session check still owns the "checking" status — and
  // bail out immediately instead of racing a second flow against it.
  transition(event: AuthEvent): boolean {
    const next = authTransition(this.state, event);
    if (next === this.state) return false;
    this.state = next;
    for (const listener of this.listeners) listener();
    return true;
  }
}

export const authMachine = new AuthMachine();
