import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useRef } from "react";

// ActionMenu's own floating-panel treatment (absolute, z-(--z-dropdown),
// mt-1, rounded-structural, border, shadow-md) — shared by every calendar
// popover (quick-create, month view's "+N more").
export const FLOATING_PANEL_CLASSES =
  "absolute z-(--z-dropdown) mt-1 rounded-structural border border-border bg-surface shadow-md";

export interface QuickCreatePopoverProps {
  date: Date;
  onCreate: () => void;
  onClose: () => void;
  triggerRef: React.RefObject<HTMLElement | null>;
}

function formatQuickCreateDate(date: Date): string {
  const hasTime = date.getHours() !== 0 || date.getMinutes() !== 0;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "long",
    ...(hasTime ? { timeStyle: "short" } : {}),
  }).format(date);
}

// Same floating-panel treatment as ActionMenu's own panel (absolute,
// z-(--z-dropdown), mt-1, rounded-structural, border, shadow-md), and the
// same Escape-closes-and-refocuses-trigger convention.
export function QuickCreatePopover({ date, onCreate, onClose, triggerRef }: QuickCreatePopoverProps): ReactNode {
  const createButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    createButtonRef.current?.focus();
  }, []);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose();
      triggerRef.current?.focus();
    }
  };

  return (
    <div
      role="dialog"
      aria-label="Quick create"
      onKeyDown={handleKeyDown}
      className={`${FLOATING_PANEL_CLASSES} min-w-56 p-3`}
    >
      <p className="mb-2 text-sm text-text">{formatQuickCreateDate(date)}</p>
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          className="rounded-control px-2 py-1 text-sm text-text-secondary hover:bg-surface-hover"
        >
          Cancel
        </button>
        <button
          ref={createButtonRef}
          type="button"
          onClick={onCreate}
          className="rounded-control bg-primary px-2 py-1 text-sm text-text-inverse hover:bg-primary-hover"
        >
          Create
        </button>
      </div>
    </div>
  );
}
