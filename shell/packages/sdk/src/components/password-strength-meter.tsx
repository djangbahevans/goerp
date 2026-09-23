import { ZxcvbnFactory } from "@zxcvbn-ts/core";
import * as zxcvbnCommon from "@zxcvbn-ts/language-common";
import * as zxcvbnEn from "@zxcvbn-ts/language-en";
import type { ReactNode } from "react";
import { useEffect, useMemo, useRef } from "react";

// Built once at module scope, not per-render/lazily — no existing shell
// component code-splits a heavy dependency (duckdb-wasm, tiptap, codemirror
// all load eagerly too), so this follows that same precedent rather than
// introducing a new lazy-loading pattern for this one component.
const zxcvbn = new ZxcvbnFactory({
  dictionary: {
    ...zxcvbnCommon.dictionary,
    ...zxcvbnEn.dictionary,
  },
  graphs: zxcvbnCommon.adjacencyGraphs,
  translations: zxcvbnEn.translations,
});

export interface PasswordStrengthMeterProps {
  password: string;
  minLength?: number | undefined;
  onValidityChange?: ((valid: boolean) => void) | undefined;
}

const SCORE_LABELS = ["Weak", "Weak", "Fair", "Good", "Strong"] as const;

// password-strength-meter.md's States table: score 0 fills 1 segment, score
// 1 and score 2 both fill 2 (distinguished from each other by color/label,
// not segment count), score 3 fills 3, score 4 fills all 4 — not a plain
// score+1, since scores 1 and 2 collide at 2 filled segments.
const SCORE_FILLED_SEGMENTS = [1, 2, 2, 3, 4] as const;

const SCORE_FILL_CLASSES = [
  "bg-danger", // 0
  "bg-danger", // 1
  "bg-warning", // 2
  "bg-success", // 3
  "bg-success", // 4
] as const;

function segmentClassName(segmentIndex: number, score: number): string {
  const filledSegments = SCORE_FILLED_SEGMENTS[score] ?? 0;
  if (segmentIndex >= filledSegments) return "bg-border";
  return SCORE_FILL_CLASSES[score] ?? "bg-border";
}

export function PasswordStrengthMeter({
  password,
  minLength = 12,
  onValidityChange,
}: PasswordStrengthMeterProps): ReactNode {
  const score = password === "" ? undefined : zxcvbn.check(password).score;
  const lengthSatisfied = password.length >= minLength;
  const valid = lengthSatisfied && score !== undefined && score >= 2;

  const onValidityChangeRef = useRef(onValidityChange);
  onValidityChangeRef.current = onValidityChange;
  useEffect(() => {
    onValidityChangeRef.current?.(valid);
  }, [valid]);

  // The live region's text is memoized on exactly the two values allowed to
  // change its announcement (password-strength-meter.md: "only re-announces
  // when the bucket or the length-satisfied boolean changes, not on every
  // keystroke") — the character count itself is deliberately left out of
  // this text so the memo has no reason to recompute on every keystroke.
  const statusText = useMemo(() => {
    const strength = score === undefined ? "" : `Password strength: ${SCORE_LABELS[score]}. `;
    const lengthText = lengthSatisfied
      ? `${minLength} characters required — met.`
      : `${minLength} characters required.`;
    return `${strength}${lengthText}`;
  }, [score, lengthSatisfied, minLength]);

  return (
    <div className="flex flex-col gap-2">
      <div aria-hidden className="flex gap-1">
        {[0, 1, 2, 3].map((segmentIndex) => (
          <span
            key={segmentIndex}
            className={`h-1 flex-1 rounded-full ${segmentClassName(segmentIndex, score ?? -1)}`}
          />
        ))}
      </div>
      <div className="flex items-center justify-between text-xs">
        <span className={lengthSatisfied ? "text-success" : "text-text-secondary"}>
          {lengthSatisfied ? "✓ " : ""}At least {minLength} characters
        </span>
        {score !== undefined && <span className="text-text-secondary">{SCORE_LABELS[score]}</span>}
      </div>
      <span role="status" aria-live="polite" className="sr-only">
        {statusText}
      </span>
    </div>
  );
}
