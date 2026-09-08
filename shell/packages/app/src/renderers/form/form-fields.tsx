import { useFieldPermission } from "@goerp/sdk/auth";
import { useId } from "react";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

// ContentEditable primary controls aren't natively labelable — <label
// htmlFor> wouldn't associate, so these get <span id> + aria-labelledby
// instead (goerp#698).
const ARIA_LABELLEDBY_FIELD_TYPES = new Set(["code", "rich_text"]);

export interface FormFieldRowProps {
  field: FormField;
  resource: string;
  record: Row;
  onChange: (patch: Record<string, unknown>) => void;
  formReadonly: boolean;
}

// No single primary control to point a label at — each already carries its
// own accessible name (per-input aria-label, or a self-labeled group).
const UNASSOCIATED_FIELD_TYPES = new Set([
  "date_range",
  "duration",
  "address",
  "location",
  "radio",
  "rating",
  "signature",
  "qr_code",
  "computed_display",
  "custom",
]);

// A field lacking read access is absent from the DOM entirely; lacking
// write access (or marked readonly/computed) renders read-only.
export function FormFieldRow({ field, resource, record, onChange, formReadonly }: FormFieldRowProps) {
  const { canRead, canWrite } = useFieldPermission(resource, field.field);
  const generatedId = useId();
  if (field.hidden || !canRead) return null;

  const readonly = formReadonly || field.readonly || field.computed || !canWrite;
  const value = readFieldValue(field, record);

  if (field.type === "separator" || field.type === "label") {
    return <FieldInput field={field} value={value} record={record} onChange={() => {}} />;
  }

  // Explicit id/htmlFor, not a wrapping <label> — DOM order (e.g. TagsField's
  // "Remove tag" buttons ahead of its input) can't hijack the association.
  const type = field.type ?? "text";
  const id = UNASSOCIATED_FIELD_TYPES.has(type) ? undefined : generatedId;
  const labelText = (
    <>
      {field.label ?? field.field}
      {field.required && <span aria-hidden="true"> *</span>}
    </>
  );

  return (
    <div style={field.span ? { gridColumn: `span ${field.span}` } : undefined}>
      {ARIA_LABELLEDBY_FIELD_TYPES.has(type) ? (
        <span id={id}>{labelText}</span>
      ) : (
        <label htmlFor={id}>{labelText}</label>
      )}
      <FieldInput
        field={field}
        value={value}
        record={record}
        disabled={readonly}
        id={id}
        onChange={(next) => (readonly ? undefined : onChange(writeFieldValue(field, next)))}
      />
      {field.help_text && <p>{field.help_text}</p>}
    </div>
  );
}
