import type { ReactNode } from "react";
import { createContext, useContext, useId } from "react";

export interface FieldControlProps {
  id: string;
  describedBy: string | undefined;
  invalid: boolean;
  required: boolean;
}

const FieldContext = createContext<FieldControlProps | null>(null);

// The enclosing FieldWrapper's wiring, or null outside one. A custom control
// applies it to its focusable element (docs/components/field-wrapper.md).
export function useFieldControl(): FieldControlProps | null {
  return useContext(FieldContext);
}

export interface FieldWrapperProps {
  label: string;
  description?: string | undefined;
  error?: string | undefined;
  required?: boolean | undefined;
  children: ReactNode;
}

export function FieldWrapper({ label, description, error, required = false, children }: FieldWrapperProps): ReactNode {
  const id = useId();
  const descriptionId = `${id}-description`;
  const errorId = `${id}-error`;
  const describedBy = joinIds(description !== undefined && descriptionId, error !== undefined && errorId);

  return (
    <div className="flex flex-col gap-1">
      <FieldLabel htmlFor={id} required={required}>
        {label}
      </FieldLabel>
      {description !== undefined && <FieldDescription id={descriptionId}>{description}</FieldDescription>}
      <FieldContext value={{ id, describedBy, invalid: error !== undefined, required }}>{children}</FieldContext>
      {error !== undefined && <FieldError id={errorId}>{error}</FieldError>}
    </div>
  );
}

export function joinIds(...ids: Array<string | false | undefined>): string | undefined {
  return ids.filter(Boolean).join(" ") || undefined;
}

// The field layout's parts (text-input.md "Field layout"), shared with the
// self-labelled fields (PasswordField, MoneyField).
export function FieldLabel({
  htmlFor,
  required = false,
  children,
}: {
  htmlFor: string;
  required?: boolean | undefined;
  children: ReactNode;
}): ReactNode {
  return (
    <label htmlFor={htmlFor} className="text-sm font-medium text-text">
      {children}
      {required && (
        <span aria-hidden="true" className="text-danger">
          {" "}
          *
        </span>
      )}
    </label>
  );
}

export function FieldDescription({ id, children }: { id: string; children: ReactNode }): ReactNode {
  return (
    <span id={id} className="text-sm text-text-secondary">
      {children}
    </span>
  );
}

export function FieldError({ id, children }: { id: string; children: ReactNode }): ReactNode {
  return (
    <span id={id} role="alert" className="text-sm text-danger">
      {children}
    </span>
  );
}

// Wiring for a self-labelled field (DateField, TagsField, ...) that also
// works inside a FieldWrapper: the wrapper's id (so its label points at this
// control) and describedBy, and invalid on either error.
export function useSelfLabelledFieldControl(
  idProp: string | undefined,
  error: string | undefined,
): {
  id: string;
  errorId: string;
  controlProps: {
    id: string;
    "aria-describedby": string | undefined;
    "aria-invalid": true | undefined;
    required: boolean | undefined;
  };
} {
  const field = useFieldControl();
  const generatedId = useId();
  const id = field?.id ?? idProp ?? generatedId;
  const errorId = `${generatedId}-error`;
  return {
    id,
    errorId,
    controlProps: {
      id,
      "aria-describedby": joinIds(field?.describedBy, error !== undefined && errorId),
      "aria-invalid": error !== undefined || field?.invalid ? true : undefined,
      required: field?.required || undefined,
    },
  };
}
