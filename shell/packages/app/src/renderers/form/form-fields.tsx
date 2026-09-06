import { useFieldPermission } from "@goerp/sdk/auth";
import type { Row } from "../list/list-view-types.js";
import { FieldInput, readFieldValue, writeFieldValue } from "./field-renderers.js";
import type { FormField } from "./form-view-types.js";

export interface FormFieldRowProps {
  field: FormField;
  resource: string;
  record: Row;
  onChange: (patch: Record<string, unknown>) => void;
  formReadonly: boolean;
}

// A field lacking read access is absent from the DOM entirely; lacking
// write access (or marked readonly/computed) renders read-only.
export function FormFieldRow({ field, resource, record, onChange, formReadonly }: FormFieldRowProps) {
  const { canRead, canWrite } = useFieldPermission(resource, field.field);
  if (field.hidden || !canRead) return null;

  const readonly = formReadonly || field.readonly || field.computed || !canWrite;
  const value = readFieldValue(field, record);

  if (field.type === "separator" || field.type === "label") {
    return <FieldInput field={field} value={value} record={record} onChange={() => {}} />;
  }

  return (
    <div style={field.span ? { gridColumn: `span ${field.span}` } : undefined}>
      {/* biome-ignore lint/a11y/noLabelWithoutControl: FieldInput always nests a real control here — its concrete element depends on field.type, which biome can't see through statically. */}
      <label>
        {field.label ?? field.field}
        {field.required && <span aria-hidden="true"> *</span>}
        <FieldInput
          field={field}
          value={value}
          record={record}
          disabled={readonly}
          onChange={(next) => (readonly ? undefined : onChange(writeFieldValue(field, next)))}
        />
      </label>
      {field.help_text && <p>{field.help_text}</p>}
    </div>
  );
}
