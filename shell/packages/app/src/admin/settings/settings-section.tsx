import { ActionButton, cn, SectionCard } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import type { ReactNode } from "react";

// A rejected field from a 422 invalid_setting or locked_setting response.
export interface FieldError {
  field: string;
  message: string;
}

export function fieldErrorOf(err: unknown): FieldError | null {
  if (!(err instanceof AppError) || err.httpStatus !== 422) return null;
  const field = err.details?.field;
  if (typeof field !== "string") return null;
  return { field, message: err.code === "locked_setting" ? "Set by your platform operator." : err.message };
}

// The message under `field`, for a section's FieldWrappers.
export function errorFor(error: FieldError | null, field: string): string | undefined {
  return error?.field === field ? error.message : undefined;
}

// Reports a failed save: a field error the section can show under its field
// is returned for that; anything else becomes a toast.
export function reportSaveError(err: unknown, fields: readonly string[]): FieldError | null {
  const fe = fieldErrorOf(err);
  if (fe && fields.some((f) => fe.field === f || fe.field.startsWith(`${f}.`))) return fe;
  toast.error(fe?.message ?? (err instanceof AppError && err.message ? err.message : "Couldn't save. Try again."));
  return null;
}

export interface SettingsSectionProps {
  title: string;
  description?: string | undefined;
  dirty: boolean;
  saving: boolean;
  onSave: () => void;
  // Shown before Save, such as "Send test email".
  actions?: ReactNode;
  // Lets content such as a table use the card's full width; form fields
  // otherwise keep to a readable measure.
  wide?: boolean | undefined;
  children: ReactNode;
}

// One card of the tenant settings page, saved on its own.
export function SettingsSection({
  title,
  description,
  dirty,
  saving,
  onSave,
  actions,
  wide = false,
  children,
}: SettingsSectionProps): ReactNode {
  return (
    <SectionCard title={title}>
      {description && <p className="mt-1 text-sm text-text-secondary">{description}</p>}
      <div className={cn("mt-4 flex flex-col gap-5", !wide && "max-w-xl")}>{children}</div>
      <div className="mt-6 flex flex-wrap items-center justify-end gap-2 border-border border-t pt-4">
        {actions}
        <ActionButton onClick={onSave} loading={saving} disabled={!dirty || saving}>
          Save
        </ActionButton>
      </div>
    </SectionCard>
  );
}

export function localeLabel(tag: string): string {
  try {
    const name = new Intl.DisplayNames([tag], { type: "language" }).of(tag);
    return name ? `${name.charAt(0).toLocaleUpperCase(tag)}${name.slice(1)}` : tag;
  } catch {
    return tag;
  }
}

// A whole number typed into a number input, or null while it isn't one.
export function parseWhole(value: string): number | null {
  return /^\d+$/.test(value.trim()) ? Number(value.trim()) : null;
}
