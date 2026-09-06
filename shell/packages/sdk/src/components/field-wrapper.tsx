import type { ReactNode } from "react";

export interface FieldWrapperProps {
  label: string;
  error?: string | undefined;
  required?: boolean | undefined;
  children: ReactNode;
}

export function FieldWrapper({ label, error, required = false, children }: FieldWrapperProps): ReactNode {
  return (
    <div className="flex flex-col gap-1">
      {/* biome-ignore lint/a11y/noLabelWithoutControl: children is a caller-supplied control (an <input>, SegmentedField, ...) nested here for implicit label association, same posture as form-fields.tsx's FormFieldRow. */}
      <label className="flex flex-col gap-1 text-fg text-sm">
        <span>
          {label}
          {required && <span aria-hidden="true"> *</span>}
        </span>
        {children}
      </label>
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
