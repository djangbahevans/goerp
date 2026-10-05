import { Button, TextInput } from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import type { KeyboardEvent, ReactNode, SubmitEvent } from "react";
import { useEffect, useRef, useState } from "react";
import type { KanbanQuickCreateField } from "./kanban-view-types.js";

export interface KanbanQuickCreateRowProps {
  groupId: string;
  fields: KanbanQuickCreateField[];
  onSubmit: (values: Record<string, string>) => Promise<void>;
}

function inputIdOf(groupId: string, fieldName: string): string {
  return `kanban-quick-create-${groupId}-${fieldName}`;
}

interface Failure {
  fieldErrors: Record<string, string>;
  message: string | undefined;
}

// A 422's field errors land on the inputs shown; errors for fields the row
// doesn't have, and every other failure, go in the alert line.
function toFailure(err: unknown, fields: KanbanQuickCreateField[]): Failure {
  if (!isAppError(err) || !err.fieldErrors) {
    return { fieldErrors: {}, message: err instanceof Error ? err.message : String(err) };
  }
  const fieldErrors: Record<string, string> = {};
  const unplaced: string[] = [];
  for (const [name, messages] of Object.entries(err.fieldErrors)) {
    const text = Array.isArray(messages) ? messages.join(" ") : String(messages);
    if (fields.some((field) => field.name === name)) fieldErrors[name] = text;
    else unplaced.push(`${name}: ${text}`);
  }
  const placed = Object.keys(fieldErrors).length > 0;
  return { fieldErrors, message: unplaced.length > 0 ? unplaced.join(" ") : placed ? undefined : err.message };
}

// The manifest's quick_create inline "+ Add" row (view-system.md §6) — a
// row inside the column, not a modal like CalendarView's quick-create-popover.tsx.
export function KanbanQuickCreateRow({ groupId, fields, onSubmit }: KanbanQuickCreateRowProps): ReactNode {
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<Record<string, string>>({});
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | undefined>();
  const [submitting, setSubmitting] = useState(false);
  const firstInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) firstInputRef.current?.focus();
  }, [open]);

  function close(): void {
    setOpen(false);
    setValues({});
    setFieldErrors({});
    setFormError(undefined);
  }

  async function handleSubmit(event: SubmitEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (submitting) return;
    const missing: Record<string, string> = {};
    for (const field of fields) {
      if (field.required && (values[field.name] ?? "").trim() === "") missing[field.name] = "This field is required.";
    }
    setFieldErrors(missing);
    setFormError(undefined);
    const firstMissing = fields.find((field) => field.name in missing);
    if (firstMissing) {
      document.getElementById(inputIdOf(groupId, firstMissing.name))?.focus();
      return;
    }
    setSubmitting(true);
    try {
      await onSubmit(values);
      close();
    } catch (err) {
      const failure = toFailure(err, fields);
      setFieldErrors(failure.fieldErrors);
      setFormError(failure.message);
    } finally {
      setSubmitting(false);
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLFormElement>): void {
    // An open dropdown inside the form handles its own Escape first.
    if (event.key === "Escape" && !event.defaultPrevented && !submitting) close();
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="w-full rounded-control p-2 text-left text-text-secondary text-xs hover:bg-surface-hover hover:text-text focus-visible:shadow-focus focus-visible:outline-none"
      >
        + Add
      </button>
    );
  }

  return (
    <form
      noValidate
      onSubmit={handleSubmit}
      onKeyDown={handleKeyDown}
      className="flex flex-col gap-2 rounded-control border border-border bg-surface p-2"
    >
      {fields.map((field, index) => {
        const inputId = inputIdOf(groupId, field.name);
        const errorId = `${inputId}-error`;
        const error = fieldErrors[field.name];
        return (
          <div key={field.name} className="flex flex-col gap-1">
            <label htmlFor={inputId} className="sr-only">
              {field.label}
            </label>
            <TextInput
              id={inputId}
              ref={index === 0 ? firstInputRef : undefined}
              required={field.required}
              invalid={error !== undefined}
              aria-describedby={error !== undefined ? errorId : undefined}
              readOnly={submitting}
              value={values[field.name] ?? ""}
              onChange={(next) => {
                setValues((prev) => ({ ...prev, [field.name]: next }));
                setFieldErrors(({ [field.name]: _cleared, ...rest }) => rest);
              }}
              placeholder={field.label}
              size="sm"
            />
            {error !== undefined && (
              <span id={errorId} role="alert" className="text-danger text-xs">
                {error}
              </span>
            )}
          </div>
        );
      })}
      {formError !== undefined && (
        <p role="alert" className="text-danger text-xs">
          {formError}
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button variant="ghost" size="sm" disabled={submitting} onClick={close}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" size="sm" loading={submitting}>
          Add
        </Button>
      </div>
    </form>
  );
}
