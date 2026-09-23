import type { ReactNode } from "react";
import { useEffect, useRef, useState } from "react";

export interface CountdownProps {
  // Starting duration in seconds — e.g. a 429 response's `retry_after`.
  seconds: number;
  format?: ((secondsRemaining: number) => string) | undefined;
  // Called once, when the countdown reaches 0.
  onComplete?: (() => void) | undefined;
}

function defaultFormat(secondsRemaining: number): string {
  return `${secondsRemaining} second${secondsRemaining === 1 ? "" : "s"}`;
}

// How often the displayed value is recomputed from elapsed wall-clock time.
// Finer than the 1-second display granularity so a late-firing tick (a
// throttled background tab delaying setInterval) still catches up to the
// correct remaining value on its next fire, rather than drifting behind.
const TICK_INTERVAL_MS = 200;

// Rounds up so a fractional `seconds` prop (the documented usage is an
// integer `retry_after`, but the prop type doesn't enforce that) renders
// consistently with every subsequent tick's own Math.ceil — otherwise the
// first tick could compute a *larger* remaining value than the initial
// render showed, making the displayed number visibly increase before it
// starts counting down.
function initialRemaining(seconds: number): number {
  return Math.max(0, Math.ceil(seconds));
}

export function Countdown({ seconds, format = defaultFormat, onComplete }: CountdownProps): ReactNode {
  const [remaining, setRemaining] = useState(() => initialRemaining(seconds));
  const onCompleteRef = useRef(onComplete);
  onCompleteRef.current = onComplete;

  useEffect(() => {
    const start = Date.now();
    setRemaining(initialRemaining(seconds));

    if (seconds <= 0) {
      onCompleteRef.current?.();
      return;
    }

    const id = setInterval(() => {
      // Anchored to real elapsed time, not a per-tick decrement — a naive
      // setInterval(1000) counter drifts slow under a throttled background
      // tab, letting the displayed countdown run behind the server's actual
      // expiry (countdown.md's "Ticking" state).
      const elapsedSeconds = (Date.now() - start) / 1000;
      const next = Math.max(0, Math.ceil(seconds - elapsedSeconds));
      setRemaining(next);
      if (next <= 0) {
        clearInterval(id);
        onCompleteRef.current?.();
      }
    }, TICK_INTERVAL_MS);

    return () => clearInterval(id);
  }, [seconds]);

  // "Countdown has no post-complete visual state of its own" (countdown.md)
  // — the caller's onComplete handler owns whatever renders next.
  if (remaining <= 0) return null;

  return <>{format(remaining)}</>;
}
