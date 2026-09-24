import { useFieldPermission } from "@goerp/sdk/auth";
import { FieldWrapper, useFieldControl } from "@goerp/sdk/components";
import { useId } from "react";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, type FieldInputProps, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

// ContentEditable/canvas primary controls aren't natively labelable —
// <label htmlFor> wouldn't associate, so these get <span id> +
// aria-labelledby instead (goerp#698). "location"'s map canvas is the same
// case (goerp#830). "custom" (goerp#752) renders an arbitrary
// module-registered component of unknown internal structure — the same
// "can't assume a single labelable native control" reasoning applies, and
// the resolved component receives the generated id as its own `id` prop to
// apply via aria-labelledby on whichever element it considers primary.
// "markdown" (goerp#858) is the same contentEditable Tiptap surface
// "rich_text"/"code" already are, just a different serialization format.
const ARIA_LABELLEDBY_FIELD_TYPES = new Set(["code", "rich_text", "markdown", "json", "location", "custom"]);

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
  "radio",
  "rating",
  "signature",
  "qr_code",
  "computed_display",
]);

// A field lacking read access is absent from the DOM entirely; lacking
// write access (or marked readonly/computed) renders read-only.
export function FormFieldRow({ field, resource, record, onChange, formReadonly }: FormFieldRowProps) {
  const { canRead, canWrite } = useFieldPermission(resource, field.field);
  const generatedId = useId();
  const conditions = useConditionEvaluator(`${resource} form`);
  if (field.hidden || !canRead || !conditions.isVisible(field.condition, `field "${field.field}" condition`, record)) {
    return null;
  }

  const readonly =
    formReadonly ||
    field.readonly ||
    field.computed ||
    !canWrite ||
    conditions.isReadonly(field.readonly_condition, `field "${field.field}" readonly_condition`, record);
  const value = readFieldValue(field, record);

  if (field.type === "separator" || field.type === "label") {
    return <FieldInput field={field} value={value} record={record} resource={resource} onChange={() => {}} />;
  }

  const type = field.type ?? "text";
  const inputProps = {
    field,
    value,
    record,
    resource,
    disabled: readonly,
    onChange: (next: unknown) => (readonly ? undefined : onChange(writeFieldValue(field, next))),
  };
  const gridStyle = field.span ? { gridColumn: `span ${field.span}` } : undefined;

  if (!ARIA_LABELLEDBY_FIELD_TYPES.has(type) && !UNASSOCIATED_FIELD_TYPES.has(type)) {
    return (
      <div style={gridStyle}>
        <FieldWrapper
          label={field.label ?? field.field}
          description={field.help_text || undefined}
          required={field.required ?? false}
        >
          <WrappedFieldInput {...inputProps} />
        </FieldWrapper>
      </div>
    );
  }

  // field-wrapper.md's label and description typography, for the field types
  // FieldWrapper's <label htmlFor> can't name: a contentEditable/canvas control
  // (aria-labelledby) or a compound one whose parts label themselves.
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
  const id = UNASSOCIATED_FIELD_TYPES.has(type) ? undefined : generatedId;

  return (
    <div className="flex flex-col gap-1" style={gridStyle}>
      {id === undefined ? (
        <span className={LABEL_CLASS_NAME}>{labelText}</span>
      ) : (
        <span id={id} className={LABEL_CLASS_NAME}>
          {labelText}
        </span>
      )}
      <FieldInput {...inputProps} id={id} />
      {field.help_text && <p className="text-sm text-text-secondary">{field.help_text}</p>}
    </div>
  );
}

const LABEL_CLASS_NAME = "text-sm font-medium text-text";

// Hands FieldWrapper's generated id to the control, for the SDK controls that
// take an `id` prop rather than reading FieldContext themselves.
function WrappedFieldInput(props: Omit<FieldInputProps, "id">) {
  const fieldControl = useFieldControl();
  return <FieldInput {...props} id={fieldControl?.id} />;
}
