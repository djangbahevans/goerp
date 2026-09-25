import * as AlertDialogPrimitive from "@radix-ui/react-alert-dialog";
import type { ChangeEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import * as v from "valibot";
import { optionalNullable } from "../schema/optional-nullable.js";
import { Button } from "./button.js";
import { Icon } from "./icon.js";
import { MODAL_OVERLAY_CLASSES } from "./modal-overlay.js";

// manifest-spec.md's SelectOption, as used by ConfirmInput — a schema, not
// just a type, so ActionConfirmInputSchema can extend it directly.
export const AlertDialogSelectOptionSchema = v.looseObject({
  value: v.string(),
  label: v.string(),
  disabled: optionalNullable(v.boolean()),
});
export type AlertDialogSelectOption = v.InferOutput<typeof AlertDialogSelectOptionSchema>;

// manifest-spec.md's ConfirmInput object.
export const AlertDialogInputSchema = v.looseObject({
  label: v.string(),
  type: v.picklist(["text", "select"] as const),
  required: optionalNullable(v.boolean()),
  placeholder: optionalNullable(v.string()),
  // Required when type is "select".
  options: optionalNullable(v.array(AlertDialogSelectOptionSchema)),
});
export type AlertDialogInput = v.InferOutput<typeof AlertDialogInputSchema>;

export interface AlertDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel?: string | undefined;
  cancelLabel?: string | undefined;
  confirmVariant?: "primary" | "danger" | undefined;
  input?: AlertDialogInput | undefined;
  // A warning or danger icon beside the title. Independent of confirmVariant.
  tone?: "default" | "warning" | "danger" | undefined;
  // A phrase the user must type exactly before confirm enables.
  requireTyping?: string | undefined;
  // Where focus goes on close, instead of the element focused when it opened.
  returnFocusTo?: HTMLElement | null | undefined;
  // Called with the collected input value (possibly "" when not required
  // and left blank), or undefined when no `input` was configured.
  onConfirm: (inputValue?: string) => void;
  onCancel: () => void;
  // Called once the dialog has finished closing and returned focus.
  onClosed?: (() => void) | undefined;
}

const TONE_ICONS = {
  warning: { name: "triangle-alert", className: "text-warning" },
  danger: { name: "circle-alert", className: "text-danger" },
} as const;

const INPUT_CLASSES =
  "mt-1 w-full rounded-control border border-border px-3 py-2 text-sm text-text focus-visible:outline-none focus-visible:shadow-focus";

// Content is the full-viewport flex-centering/focus-trap boundary; the
// visible panel is a plain inner div, so the boundary can size to the whole
// viewport (required for centering) independently of the panel's own
// max-width/max-height.
const CONTENT_CLASSES =
  "fixed inset-0 z-(--z-modal) flex items-center justify-center p-4 focus:outline-none data-[state=open]:animate-[alert-dialog-content-show_var(--duration-slow)_ease-out] data-[state=closed]:animate-[alert-dialog-content-hide_var(--duration-slow)_ease-in] motion-reduce:data-[state=open]:animate-[fade-in_var(--duration-slow)_ease-out] motion-reduce:data-[state=closed]:animate-[fade-out_var(--duration-slow)_ease-in]";

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
  tone = "default",
  requireTyping,
  returnFocusTo,
  onConfirm,
  onCancel,
  onClosed,
}: AlertDialogProps): ReactNode {
  const [inputValue, setInputValue] = useState("");
  const [typed, setTyped] = useState("");
  // Radix's own close-auto-focus restores focus via a `triggerRef` that only
  // gets populated by an `AlertDialogPrimitive.Trigger` — this component is
  // fully open-controlled and never renders one, so that ref stays null and
  // Radix's built-in restoration silently no-ops. Captured and restored by
  // hand instead, via onCloseAutoFocus below.
  const triggerRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (open) {
      triggerRef.current =
        returnFocusTo !== undefined
          ? returnFocusTo
          : document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null;
      setInputValue("");
      setTyped("");
    }
  }, [open, returnFocusTo]);

  const confirmDisabled =
    (input?.required === true && inputValue.trim() === "") || (requireTyping !== undefined && typed !== requireTyping);
  const toneIcon = tone === "default" ? undefined : TONE_ICONS[tone];

  return (
    <AlertDialogPrimitive.Root
      open={open}
      // Fires on Escape and on a Cancel-button click — Cancel is Radix's
      // own Close primitive under the hood, so it triggers this the same
      // way Escape does; Confirm deliberately isn't wired through Radix's
      // Action primitive (which would trigger this identically), since
      // confirming and cancelling must never both fire from one click.
      onOpenChange={(next) => {
        if (!next) onCancel();
      }}
    >
      <AlertDialogPrimitive.Portal>
        <AlertDialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <AlertDialogPrimitive.Content
          aria-modal="true"
          className={CONTENT_CLASSES}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
            onClosed?.();
          }}
        >
          <div className="flex max-h-[90vh] w-full max-w-120 flex-col rounded-structural bg-surface shadow-lg">
            <div className="flex items-start gap-3 px-6 pt-6">
              {toneIcon && (
                <Icon
                  name={toneIcon.name}
                  size={20}
                  className={`mt-0.5 shrink-0 ${toneIcon.className}`}
                  aria-hidden="true"
                />
              )}
              <AlertDialogPrimitive.Title className="text-lg font-semibold text-text">
                {title}
              </AlertDialogPrimitive.Title>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
              <AlertDialogPrimitive.Description className="text-base text-text-secondary">
                {description}
              </AlertDialogPrimitive.Description>
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
              {requireTyping !== undefined && (
                <label className="mt-4 block text-sm text-text">
                  Type <span className="font-mono font-semibold">{requireTyping}</span> to confirm
                  <input
                    type="text"
                    className={INPUT_CLASSES}
                    value={typed}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(e: ChangeEvent<HTMLInputElement>) => setTyped(e.target.value)}
                  />
                </label>
              )}
            </div>
            <div className="flex justify-end gap-2 px-6 pb-6 pt-2">
              <AlertDialogPrimitive.Cancel asChild>
                <Button variant="ghost">{cancelLabel}</Button>
              </AlertDialogPrimitive.Cancel>
              <Button
                variant={confirmVariant}
                disabled={confirmDisabled}
                onClick={() => onConfirm(input ? inputValue : undefined)}
              >
                {confirmLabel}
              </Button>
            </div>
          </div>
        </AlertDialogPrimitive.Content>
      </AlertDialogPrimitive.Portal>
    </AlertDialogPrimitive.Root>
  );
}
