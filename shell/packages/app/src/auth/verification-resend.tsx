import { resendVerificationEmail, type VerificationEmailRequest } from "@goerp/sdk/auth";
import { Countdown } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { type ReactNode, useState } from "react";

const COOLDOWN_SECONDS = 60;
// Used when a 429 arrives without a parseable Retry-After header.
const DEFAULT_LOCKOUT_SECONDS = 60;

export type ResendState =
  | { kind: "idle" }
  | { kind: "sending" }
  | { kind: "sent"; key: number; coolingDown: boolean }
  | { kind: "locked"; seconds: number; key: number }
  | { kind: "failed"; message: string };

export type ResendVerification = (input: VerificationEmailRequest) => Promise<void>;

export interface VerificationResend {
  state: ResendState;
  sending: boolean;
  coolingDown: boolean;
  send: (input: VerificationEmailRequest) => Promise<void>;
  endCooldown: () => void;
}

// The resend flow shared by the verify-email page's expired state and the
// login page's email_verification_required error (shell-ux.md §2.1, §2.10).
export function useVerificationResend(resend: ResendVerification = resendVerificationEmail): VerificationResend {
  const [state, setState] = useState<ResendState>({ kind: "idle" });

  const send = async (input: VerificationEmailRequest) => {
    setState({ kind: "sending" });
    try {
      await resend(input);
      setState({ kind: "sent", key: Date.now(), coolingDown: true });
    } catch (err) {
      if (isAppError(err) && err.isRateLimited()) {
        const retryAfter = err.details?.retryAfter;
        const seconds = typeof retryAfter === "number" && retryAfter > 0 ? retryAfter : DEFAULT_LOCKOUT_SECONDS;
        setState({ kind: "locked", seconds, key: Date.now() });
        return;
      }
      setState({
        kind: "failed",
        message: isAppError(err)
          ? "Something went wrong. Try again."
          : "Couldn't reach the server. Check your connection and try again.",
      });
    }
  };

  return {
    state,
    sending: state.kind === "sending",
    coolingDown: (state.kind === "sent" && state.coolingDown) || state.kind === "locked",
    send,
    endCooldown: () =>
      setState((s) => {
        if (s.kind === "sent") return { ...s, coolingDown: false };
        if (s.kind === "locked") return { kind: "idle" };
        return s;
      }),
  };
}

// One live region for the confirmation and the cooldown, so a screen reader
// announces the send without focus moving. The confirmation never says
// whether an account exists: the endpoint answers 200 either way.
export function ResendStatus({ resend }: { resend: VerificationResend }): ReactNode {
  const { state, endCooldown } = resend;
  return (
    <div
      role="status"
      aria-live="polite"
      className={`text-sm empty:hidden ${state.kind === "failed" || state.kind === "locked" ? "text-danger" : "text-text-secondary"}`}
    >
      {state.kind === "failed" && state.message}
      {state.kind === "locked" && (
        <>
          Too many attempts. Try again in <Countdown key={state.key} seconds={state.seconds} onComplete={endCooldown} />
          .
        </>
      )}
      {state.kind === "sent" && (
        <>
          If your account still needs verifying, a new link is on its way.
          {state.coolingDown && (
            <>
              {" "}
              Resend again in <Countdown key={state.key} seconds={COOLDOWN_SECONDS} onComplete={endCooldown} />.
            </>
          )}
        </>
      )}
    </div>
  );
}
