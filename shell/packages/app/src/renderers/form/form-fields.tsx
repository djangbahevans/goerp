import { useFieldPermission, useOptionalPermission } from "@goerp/sdk/auth";
import { FieldWrapper, useFieldControl } from "@goerp/sdk/components";
import { type CSSProperties, useId } from "react";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import type { Row } from "../list/list-view-types.js";
import { useModelFields } from "../record-values.js";
import { FieldDisplay } from "./field-display.js";
import { FieldInput, type FieldInputProps, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

// ContentEditable, canvas, and custom controls need aria-labelledby rather than label htmlFor.
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
  "computed_display",
]);

export function FormFieldRow({ field, resource, record, onChange, formReadonly }: FormFieldRowProps) {
  const { canRead, canWrite } = useFieldPermission(resource, field.field);
  const allowed = useOptionalPermission(field.permission);
  const fields = useModelFields(resource);
  const generatedId = useId();
  const conditions = useConditionEvaluator(`${resource} form`);
  if (
    field.hidden ||
    !canRead ||
    !allowed ||
    !conditions.isVisible(field.condition, `field "${field.field}" condition`, record)
  ) {
    return null;
  }

  const readonly =
    formReadonly ||
    field.type === "qr_code" ||
    field.readonly ||
    field.computed ||
    !canWrite ||
    conditions.isReadonly(field.readonly_condition, `field "${field.field}" readonly_condition`, record);
  const value = readFieldValue(field, record, fields);

  if (field.type === "separator" || field.type === "label") {
    return <FieldInput field={field} value={value} record={record} resource={resource} onChange={() => {}} />;
  }

  const type = field.type ?? "text";
  // A span only applies from 768px up: below it the section is one column, and
  // a span would add an implicit second one.
  const spanClassName = field.span ? "md:[grid-column:span_var(--field-span)]" : undefined;
  const spanStyle = field.span ? ({ "--field-span": field.span } as CSSProperties) : undefined;

  if (readonly) {
    return (
      <dl
        className={spanClassName ? `flex min-w-0 flex-col gap-1 ${spanClassName}` : "flex min-w-0 flex-col gap-1"}
        style={spanStyle}
      >
        <dt className={LABEL_CLASS_NAME}>{field.label ?? field.field}</dt>
        <dd className={DISPLAY_VALUE_CLASS_NAME}>
          <FieldDisplay field={field} record={record} resource={resource} />
        </dd>
      </dl>
    );
  }

  const inputProps = {
    field,
    value,
    record,
    resource,
    onChange: (next: unknown) => onChange(writeFieldValue(field, next)),
  };
  if (type === "boolean") {
    return (
      <div className={spanClassName} style={spanStyle} data-field={field.field}>
        <FieldInput {...inputProps} />
      </div>
    );
  }

  if (!ARIA_LABELLEDBY_FIELD_TYPES.has(type) && !UNASSOCIATED_FIELD_TYPES.has(type)) {
    return (
      <div className={spanClassName} style={spanStyle} data-field={field.field}>
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
    <div
      className={spanClassName ? `flex flex-col gap-1 ${spanClassName}` : "flex flex-col gap-1"}
      style={spanStyle}
      data-field={field.field}
    >
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

// A displayed link (mailto, tel, URL, related record) reads as one, and below 768px is at least 44px tall to tap.
const DISPLAY_VALUE_CLASS_NAME =
  "text-base text-text [&_a]:text-primary [&_a]:underline [&_a]:underline-offset-2 max-md:[&_a]:inline-flex max-md:[&_a]:min-h-11 max-md:[&_a]:items-center";

// Hands FieldWrapper's generated id to the control, for the SDK controls that
// take an `id` prop rather than reading FieldContext themselves.
function WrappedFieldInput(props: Omit<FieldInputProps, "id">) {
  const fieldControl = useFieldControl();
  return <FieldInput {...props} id={fieldControl?.id} />;
}
