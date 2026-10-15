import { apiClient } from "@goerp/sdk";
import { useAuth } from "@goerp/sdk/auth";
import {
  Button,
  Checkbox,
  DateField,
  FieldWrapper,
  MODAL_CONTENT_CLASSES,
  MODAL_OVERLAY_CLASSES,
  MoneyField,
  PhoneInput,
  Select,
  TextArea,
  TextInput,
} from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { dispatch } from "@goerp/sdk/react";
import { type DialogField, type ModalEntry, modalManager, useOnline } from "@goerp/sdk/realtime";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { type ReactNode, type SubmitEvent, useRef, useState } from "react";
import { type DialogValue, dialogRequestBody, initialDialogValues, isDialogFieldFilled } from "./dialog-values.js";

type DialogEntry = Extract<ModalEntry, { kind: "dialog" }>;

interface SubmitError {
  message: string;
  fields: Record<string, string>;
}

function toSubmitError(err: unknown): SubmitError {
  if (!isAppError(err)) {
    console.error("ui: dialog submit failed", err);
    return { message: "Something went wrong. Try again.", fields: {} };
  }
  const fields = Object.fromEntries(
    Object.entries(err.fieldErrors ?? {}).flatMap(([name, messages]) => (messages[0] ? [[name, messages[0]]] : [])),
  );
  return { message: err.message, fields };
}

function parseISODate(value: string): Date | undefined {
  if (value === "") return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function DialogFieldInput({
  field,
  value,
  error,
  currency,
  onChange,
}: {
  field: DialogField;
  value: DialogValue;
  error: string | undefined;
  currency: string | undefined;
  onChange: (value: DialogValue) => void;
}): ReactNode {
  const text = typeof value === "string" ? value : "";
  if (field.type === "boolean") {
    return (
      <Checkbox
        checked={value === true}
        onChange={onChange}
        label={field.label}
        description={field.help}
        error={error}
        required={field.required}
      />
    );
  }
  let control: ReactNode;
  switch (field.type) {
    case "textarea":
      control = <TextArea value={text} placeholder={field.placeholder} onChange={onChange} />;
      break;
    case "number":
      control = <TextInput type="number" value={text} placeholder={field.placeholder} onChange={onChange} />;
      break;
    case "monetary":
      control = (
        <MoneyField value={typeof value === "number" ? value : undefined} currency={currency} onChange={onChange} />
      );
      break;
    case "phone":
      control = <PhoneInput value={text} placeholder={field.placeholder} onChange={onChange} />;
      break;
    case "date":
      control = (
        <DateField
          value={parseISODate(text)}
          onChange={(date) => onChange(date ? date.toISOString().slice(0, 10) : "")}
        />
      );
      break;
    case "select":
      control = (
        <Select
          options={field.options ?? []}
          value={text}
          placeholder={field.placeholder}
          onChange={(next) => onChange(typeof next === "string" ? next : "")}
        />
      );
      break;
    default:
      control = <TextInput value={text} placeholder={field.placeholder} onChange={onChange} />;
  }
  return (
    <FieldWrapper label={field.label} description={field.help} error={error} required={field.required}>
      {control}
    </FieldWrapper>
  );
}

// The open_dialog instruction's data-collection dialog. A failed submit keeps
// it open with the error shown inline; a successful one runs the response's
// instructions before the dialog closes.
export function DialogModal({ entry }: { entry: DialogEntry }): ReactNode {
  const { config } = entry;
  const [values, setValues] = useState(() => initialDialogValues(config));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<SubmitError | null>(null);
  const online = useOnline();
  const currency = useAuth().tenant?.defaultCurrency || undefined;
  // Captured before Radix moves focus into the dialog; Radix restores focus
  // only to a Dialog.Trigger, which a server-opened dialog has none of.
  const returnFocus = useRef<HTMLElement | null>(null);

  const missingRequired = config.fields.some(
    (field) => field.required === true && !isDialogFieldFilled(field, values[field.name]),
  );

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitting || missingRequired || !online) return;
    setSubmitting(true);
    setError(null);
    const { method, path } = config.action;
    try {
      await modalManager.submitDialog(entry.id, () =>
        dispatch(apiClient, method, path, dialogRequestBody(config.fields, values)),
      );
    } catch (err) {
      setError(toSubmitError(err));
      setSubmitting(false);
    }
  }

  return (
    <DialogPrimitive.Root
      open
      onOpenChange={(open) => {
        if (!open && !submitting) modalManager.close(entry.id);
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className={MODAL_CONTENT_CLASSES}
          onOpenAutoFocus={() => {
            returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            returnFocus.current?.focus();
          }}
          onInteractOutside={(event) => event.preventDefault()}
        >
          <form
            noValidate
            onSubmit={(event) => void submit(event)}
            className="flex max-h-[90vh] w-full max-w-120 flex-col rounded-structural bg-surface shadow-lg"
          >
            <DialogPrimitive.Title className="px-6 pt-6 font-semibold text-lg text-text">
              {config.title}
            </DialogPrimitive.Title>
            <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-6 py-4">
              {error && (
                <p role="alert" className="rounded-control bg-danger-subtle px-3 py-2 text-danger text-sm">
                  {error.message}
                </p>
              )}
              {config.fields.map((field) => (
                <DialogFieldInput
                  key={field.name}
                  field={field}
                  value={values[field.name]}
                  error={error?.fields[field.name]}
                  currency={currency}
                  onChange={(value) => setValues((prev) => ({ ...prev, [field.name]: value }))}
                />
              ))}
            </div>
            <div className="flex justify-end gap-2 px-6 pt-2 pb-6">
              <DialogPrimitive.Close asChild>
                <Button variant="ghost" disabled={submitting}>
                  Cancel
                </Button>
              </DialogPrimitive.Close>
              <Button type="submit" variant="primary" loading={submitting} disabled={missingRequired || !online}>
                {config.submitLabel}
              </Button>
            </div>
          </form>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
