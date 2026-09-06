import type { ChangeEvent, ReactNode } from "react";
import { useEffect, useId, useState } from "react";

// manifest-spec.md's SelectOption, as used by ConfirmInput.
export interface AlertDialogSelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

// manifest-spec.md's ConfirmInput object.
export interface AlertDialogInput {
  label: string;
  type: "text" | "select";
  required?: boolean;
  placeholder?: string;
  // Required when type is "select".
  options?: AlertDialogSelectOption[];
}

export interface AlertDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel?: string | undefined;
  cancelLabel?: string | undefined;
  confirmVariant?: "primary" | "danger" | undefined;
  input?: AlertDialogInput | undefined;
  // Called with the collected input value (possibly "" when not required
  // and left blank), or undefined when no `input` was configured.
  onConfirm: (inputValue?: string) => void;
  onCancel: () => void;
}

// manifest-spec.md §9.1 "ConfirmDialog object": "Rendered via the SDK's
// AlertDialog component — named to avoid colliding with this manifest
// object, not a separate implementation." The generic renderer's own
// manifest-driven `confirm` handling renders this component directly.
export function AlertDialog({
  open,
  title,
  description,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  confirmVariant = "primary",
  input,
  onConfirm,
  onCancel,
}: AlertDialogProps): ReactNode {
  const titleId = useId();
  const [inputValue, setInputValue] = useState("");

  useEffect(() => {
    if (open) setInputValue("");
  }, [open]);

  if (!open) return null;

  const confirmDisabled = input?.required === true && inputValue.trim() === "";

  return (
    <div role="alertdialog" aria-modal="true" aria-labelledby={titleId}>
      <h2 id={titleId}>{title}</h2>
      <p>{description}</p>
      {input && (
        // biome-ignore lint/a11y/noLabelWithoutControl: the label always nests a real select or input below, depending on input.type.
        <label>
          {input.label}
          {input.type === "select" ? (
            <select value={inputValue} onChange={(e: ChangeEvent<HTMLSelectElement>) => setInputValue(e.target.value)}>
              <option value="">—</option>
              {(input.options ?? []).map((option) => (
                <option key={option.value} value={option.value} disabled={option.disabled}>
                  {option.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              type="text"
              value={inputValue}
              placeholder={input.placeholder}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setInputValue(e.target.value)}
            />
          )}
        </label>
      )}
      <button type="button" onClick={onCancel}>
        {cancelLabel}
      </button>
      <button
        type="button"
        data-variant={confirmVariant}
        disabled={confirmDisabled}
        onClick={() => onConfirm(input ? inputValue : undefined)}
      >
        {confirmLabel}
      </button>
    </div>
  );
}
