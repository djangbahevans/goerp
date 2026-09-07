import type { ChangeEvent, ReactNode } from "react";
import { useEffect, useId, useState } from "react";
import { actionButtonClassName } from "./action-button-styles.js";

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

const INPUT_CLASSES =
  "mt-1 w-full rounded-control border border-border px-3 py-2 text-sm text-text focus-visible:outline-none focus-visible:shadow-focus";

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
    <div className="fixed inset-0 z-(--z-modal) flex items-center justify-center bg-overlay p-4">
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="flex max-h-[90vh] w-full max-w-120 flex-col rounded-structural bg-surface shadow-lg"
      >
        <div className="px-6 pt-6">
          <h2 id={titleId} className="text-lg font-semibold text-text">
            {title}
          </h2>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
          <p className="text-base text-text-secondary">{description}</p>
          {input && (
            // biome-ignore lint/a11y/noLabelWithoutControl: the label always nests a real select or input below, depending on input.type.
            <label className="mt-4 block text-sm text-text">
              {input.label}
              {input.type === "select" ? (
                <select
                  className={INPUT_CLASSES}
                  value={inputValue}
                  onChange={(e: ChangeEvent<HTMLSelectElement>) => setInputValue(e.target.value)}
                >
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
                  className={INPUT_CLASSES}
                  value={inputValue}
                  placeholder={input.placeholder}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setInputValue(e.target.value)}
                />
              )}
            </label>
          )}
        </div>
        <div className="flex justify-end gap-2 px-6 pb-6 pt-2">
          <button type="button" className={actionButtonClassName("ghost", "md")} onClick={onCancel}>
            {cancelLabel}
          </button>
          <button
            type="button"
            data-variant={confirmVariant}
            data-disabled={confirmDisabled ? "true" : undefined}
            disabled={confirmDisabled}
            className={actionButtonClassName(confirmVariant, "md")}
            onClick={() => onConfirm(input ? inputValue : undefined)}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
