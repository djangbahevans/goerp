import { cn, fieldInputClassName } from "@goerp/sdk/components";
import { type ChangeEvent, type ReactNode, type Ref, useCallback, useEffect, useId, useRef } from "react";

export interface VerificationCodeInputProps {
  label: string;
  // Digits only, at most `length` long.
  value: string;
  onChange: (value: string) => void;
  // Fires when a user edit completes the code — never from a `value` prop change.
  onComplete?: ((code: string) => void) | undefined;
  length?: number | undefined;
  description?: string | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
  autoFocus?: boolean | undefined;
  ref?: Ref<HTMLInputElement> | undefined;
}

export function sanitizeCode(raw: string, length: number): string {
  return raw.replace(/\D/g, "").slice(0, length);
}

// Letter-spacing also trails the last digit; the matching text-indent
// re-centers the code.
const CODE_TYPE_CLASSES =
  "w-full text-center text-3xl font-medium leading-[1.25] tabular-nums tracking-[0.5em] indent-[0.5em]";
const SHAKE_CLASS = "motion-safe:animate-[verification-code-shake_var(--duration-base)_ease-out]";

export function VerificationCodeInput({
  label,
  value,
  onChange,
  onComplete,
  length = 6,
  description,
  error,
  disabled = false,
  autoFocus = false,
  ref,
}: VerificationCodeInputProps): ReactNode {
  const id = useId();
  const descriptionId = `${id}-description`;
  const errorId = `${id}-error`;
  const inputRef = useRef<HTMLInputElement | null>(null);

  const setRefs = useCallback(
    (node: HTMLInputElement | null) => {
      inputRef.current = node;
      if (typeof ref === "function") ref(node);
      else if (ref) ref.current = node;
    },
    [ref],
  );

  // Restarts the shake for each new error message: clearing the animation
  // and forcing a reflow lets the same keyframe run again on the same node.
  useEffect(() => {
    const node = inputRef.current;
    if (!error || !node) return;
    node.style.animation = "none";
    void node.offsetWidth;
    node.style.animation = "";
  }, [error]);

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    const next = sanitizeCode(event.target.value, length);
    onChange(next);
    if (next.length === length && next !== value) onComplete?.(next);
  };

  const describedBy = [description ? descriptionId : null, error ? errorId : null].filter(Boolean).join(" ");

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-sm font-medium text-text">
        {label}
      </label>
      {description && (
        <span id={descriptionId} className="text-sm text-text-secondary">
          {description}
        </span>
      )}
      <input
        ref={setRefs}
        id={id}
        type="text"
        inputMode="numeric"
        autoComplete="one-time-code"
        autoCorrect="off"
        spellCheck={false}
        // biome-ignore lint/a11y/noAutofocus: the code field is the MFA page's single task (verification-code-input.md "Accessibility").
        autoFocus={autoFocus}
        value={value}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy || undefined}
        onChange={handleChange}
        className={cn(
          fieldInputClassName(Boolean(error), "input", "sans", "auto"),
          CODE_TYPE_CLASSES,
          error && SHAKE_CLASS,
        )}
      />
      {error && (
        <span id={errorId} role="alert" className="text-danger text-sm">
          {error}
        </span>
      )}
    </div>
  );
}
