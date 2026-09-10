import type { FormEvent, KeyboardEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import type { KanbanQuickCreateField } from "./kanban-view-types.js";

export interface KanbanQuickCreateRowProps {
  groupId: string;
  fields: KanbanQuickCreateField[];
  onSubmit: (values: Record<string, string>) => void;
}

// The manifest's quick_create inline "+ Add" row (view-system.md §6) — a
// row inside the column, not a modal like CalendarView's quick-create-popover.tsx.
export function KanbanQuickCreateRow({ groupId, fields, onSubmit }: KanbanQuickCreateRowProps): ReactNode {
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<Record<string, string>>({});
  const firstInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) firstInputRef.current?.focus();
  }, [open]);

  function close(): void {
    setOpen(false);
    setValues({});
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    onSubmit(values);
    close();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLFormElement>): void {
    if (event.key === "Escape") close();
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="w-full rounded-control p-2 text-left text-text-secondary text-xs hover:bg-surface-hover hover:text-text focus-visible:outline-none focus-visible:shadow-focus"
      >
        + Add
      </button>
    );
  }

  return (
    <form
      onSubmit={handleSubmit}
      onKeyDown={handleKeyDown}
      className="flex flex-col gap-2 rounded-control border border-border bg-surface p-2"
    >
      {fields.map((field, index) => {
        const inputId = `kanban-quick-create-${groupId}-${field.name}`;
        return (
          <div key={field.name}>
            <label htmlFor={inputId} className="sr-only">
              {field.label}
            </label>
            <input
              id={inputId}
              ref={index === 0 ? firstInputRef : undefined}
              // Only the first field is required — view-system.md §6 leaves
              // quick_create_fields' own validation unspecified, but an
              // entirely-blank submission is worth blocking regardless.
              required={index === 0}
              value={values[field.name] ?? ""}
              onChange={(event) => setValues((prev) => ({ ...prev, [field.name]: event.target.value }))}
              placeholder={field.label}
              className="w-full rounded-control border border-border bg-bg px-2 py-1 text-sm text-text focus-visible:outline-none focus-visible:shadow-focus"
            />
          </div>
        );
      })}
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={close}
          className="rounded-control px-2 py-1 text-sm text-text-secondary hover:bg-surface-hover"
        >
          Cancel
        </button>
        <button
          type="submit"
          className="rounded-control bg-primary px-2 py-1 text-sm text-text-inverse hover:bg-primary-hover"
        >
          Add
        </button>
      </div>
    </form>
  );
}
