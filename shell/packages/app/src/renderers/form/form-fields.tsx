import { useFieldPermission } from "@goerp/sdk/auth";
import { FieldWrapper } from "@goerp/sdk/components";
import { useId } from "react";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

// ContentEditable primary controls aren't natively labelable — <label
// htmlFor> wouldn't associate, so these get <span id> + aria-labelledby
// instead (goerp#698). "json" joined this set alongside "code" once it
// moved onto the same CodeField (goerp#742) — a plain <textarea> didn't
// need it, but CodeField's CodeMirror host isn't a native form control
// either.
const ARIA_LABELLEDBY_FIELD_TYPES = new Set(["code", "rich_text", "json"]);

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

// Whether this field reaches RelationPicker at all, and as `multiple` —
// mirrored from FieldInput's render switch (field-renderers.tsx): select/
// multi_select only route through RelationPicker once field.resource is
// set (otherwise it's the plain, safe-for-any-multiplicity Select), and
// many2many always resolves to multiple regardless of field.multiple.
function reachesRelationPickerAsMultiple(field: FormField): boolean {
  const type = field.type;
  if (type === "many2many") return true;
  if (type === "relation" || type === "user_select") return field.multiple === true;
  if ((type === "select" || type === "multi_select") && field.resource !== undefined) {
    return field.multiple === true || type === "multi_select";
  }
  return false;
}

// FieldWrapper nests children inside its own <label> (implicit
// association) — unsafe for TagsField and a `multiple` RelationPicker,
// both of which render a "Remove" chip button ahead of their actual
// <input> whenever selected values exist (relation-picker.tsx's own
// `selected` array is only populated when `multiple`; a single relation's
// value renders inside the input itself, so it's unaffected). Those keep
// the explicit id/htmlFor path below instead — see form-fields.test.tsx's
// "label association" tests, which verify this per field type/multiplicity
// rather than assume it.
function usesImplicitLabelWrap(field: FormField): boolean {
  const type = field.type ?? "text";
  if (ARIA_LABELLEDBY_FIELD_TYPES.has(type) || UNASSOCIATED_FIELD_TYPES.has(type) || type === "tags") return false;
  return !reachesRelationPickerAsMultiple(field);
}

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

  // Explicit id/htmlFor, not a wrapping <label>, for the field types where
  // that's the only safe association — DOM order (e.g. TagsField's "Remove
  // tag" buttons ahead of its input) can't hijack it.
  const type = field.type ?? "text";
  const id = UNASSOCIATED_FIELD_TYPES.has(type) ? undefined : generatedId;

  const input = (
    <FieldInput
      field={field}
      value={value}
      record={record}
      disabled={readonly}
      id={id}
      onChange={(next) => (readonly ? undefined : onChange(writeFieldValue(field, next)))}
    />
  );
  const helpText = field.help_text && <p className="text-xs text-text-secondary">{field.help_text}</p>;

  if (usesImplicitLabelWrap(field)) {
    return (
      <div className="flex flex-col gap-1" style={field.span ? { gridColumn: `span ${field.span}` } : undefined}>
        {/* field-wrapper.md: required only marks the label visually —
            required/aria-required on the control itself isn't wired
            through FieldInput yet, a pre-existing gap this doesn't newly
            introduce (the old inline "*" span had the same gap). */}
        <FieldWrapper label={field.label ?? field.field} required={field.required ?? false}>
          {input}
        </FieldWrapper>
        {helpText}
      </div>
    );
  }

  // field-wrapper.md's own label typography/spacing, reproduced here (not
  // FieldWrapper itself — that's the implicit-wrap component this path
  // exists to avoid) so a field that can't safely use FieldWrapper still
  // looks like every other one instead of falling back to an unstyled
  // browser-default label.
  const labelText = (
    <>
      {field.label ?? field.field}
      {field.required && (
        <span aria-hidden="true" className="text-danger">
          {" "}
          *
        </span>
      )}
    </>
  );
  const labelClassName = "text-sm font-medium text-text";

  return (
    <div className="flex flex-col gap-1" style={field.span ? { gridColumn: `span ${field.span}` } : undefined}>
      {ARIA_LABELLEDBY_FIELD_TYPES.has(type) ? (
        <span id={id} className={labelClassName}>
          {labelText}
        </span>
      ) : (
        <label htmlFor={id} className={labelClassName}>
          {labelText}
        </label>
      )}
      {input}
      {helpText}
    </div>
  );
}
