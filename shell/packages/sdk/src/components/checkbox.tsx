import { Check, Minus } from "lucide-react";
import type { ChangeEvent, ComponentPropsWithRef, ReactNode, Ref } from "react";
import { useCallback, useId, useLayoutEffect, useRef } from "react";
import { cn } from "./cn.js";
import { FieldDescription, FieldError, joinIds } from "./field-wrapper.js";

type NativeCheckboxProps = Omit<
  ComponentPropsWithRef<"input">,
  "type" | "checked" | "defaultChecked" | "onChange" | "className" | "style" | "children" | "size"
>;

export interface CheckboxProps extends NativeCheckboxProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: ReactNode;
  labelHidden?: boolean | undefined;
  description?: string | undefined;
  indeterminate?: boolean | undefined;
  error?: string | undefined;
  disabled?: boolean | undefined;
}

// docs/components/checkbox.md.
export function Checkbox({
  checked,
  onChange,
  label,
  labelHidden = false,
  description,
  indeterminate = false,
  error,
  disabled = false,
  ref,
  "aria-describedby": ariaDescribedBy,
  className: _className,
  style: _style,
  ...rest
}: CheckboxProps & { className?: unknown; style?: unknown }): ReactNode {
  const generatedId = useId();
  const descriptionId = `${generatedId}-description`;
  const errorId = `${generatedId}-error`;
  const invalid = error !== undefined;
  const filled = checked || indeterminate;

  const inputRef = useRef<HTMLInputElement | null>(null);
  const setInputRef = useCallback(
    (node: HTMLInputElement | null) => {
      inputRef.current = node;
      assignRef(ref, node);
    },
    [ref],
  );
  // `indeterminate` is a DOM property with no HTML attribute. A click clears
  // it natively, so it is reapplied after every render.
  useLayoutEffect(() => {
    if (inputRef.current) inputRef.current.indeterminate = indeterminate;
  });

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    event.currentTarget.indeterminate = indeterminate;
    onChange(indeterminate || event.currentTarget.checked);
  };

  const Glyph = indeterminate ? Minus : checked ? Check : undefined;

  return (
    <span className="inline-flex flex-col items-start gap-1 self-start">
      <label
        className={cn(
          "group inline-flex items-start gap-2 text-sm font-normal text-text",
          disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer",
        )}
      >
        <span className="flex h-5 shrink-0 items-center">
          <input
            {...rest}
            ref={setInputRef}
            type="checkbox"
            checked={checked}
            disabled={disabled}
            aria-checked={indeterminate ? "mixed" : undefined}
            aria-invalid={invalid || undefined}
            aria-describedby={joinIds(
              description !== undefined && descriptionId,
              error !== undefined && errorId,
              ariaDescribedBy,
            )}
            onChange={handleChange}
            className="peer sr-only"
          />
          <span
            aria-hidden="true"
            data-state={indeterminate ? "indeterminate" : checked ? "checked" : "unchecked"}
            className={cn(
              "flex size-4 items-center justify-center rounded-structural border text-text-inverse",
              "transition-colors duration-(--duration-fast) ease-out motion-reduce:transition-none",
              "peer-focus-visible:shadow-focus",
              filled ? "bg-primary" : "bg-surface",
              invalid ? "border-danger" : filled ? "border-primary" : "border-border-control",
              !disabled &&
                (filled
                  ? "group-hover:border-primary-hover group-hover:bg-primary-hover"
                  : !invalid && "group-hover:border-primary"),
              invalid && filled && !disabled && "group-hover:border-danger",
            )}
          >
            {Glyph && <Glyph size={12} strokeWidth={3} />}
          </span>
        </span>
        <span className={labelHidden ? "sr-only" : undefined}>{label}</span>
      </label>
      {(description !== undefined || error !== undefined) && (
        <span className="flex flex-col gap-1 ps-6">
          {description !== undefined && <FieldDescription id={descriptionId}>{description}</FieldDescription>}
          {error !== undefined && <FieldError id={errorId}>{error}</FieldError>}
        </span>
      )}
    </span>
  );
}

function assignRef<T>(ref: Ref<T> | undefined, value: T | null): void {
  if (typeof ref === "function") ref(value);
  else if (ref) ref.current = value;
}
