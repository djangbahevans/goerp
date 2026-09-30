import { useTenant } from "@goerp/sdk/auth";
import { ActionButton, FieldWrapper, IconPicker, TextInput } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { type AdminActivityType, useCreateActivityType, useUpdateActivityType } from "@goerp/sdk/react";
import { type ReactNode, type SubmitEvent, useEffect, useState } from "react";
import { SideSheet } from "../../chrome/side-sheet.js";
import { LocaleFieldSet } from "./locale-field-set.js";
import { slugifyActivityTypeKey } from "./slugify-activity-type-key.js";

export interface ActivityTypeSheetProps {
  open: boolean;
  onClose: () => void;
  // null adds a type; otherwise edits it.
  editing: AdminActivityType | null;
}

interface FormErrors {
  label?: string;
  key?: string;
  icon?: string;
  defaultDueDays?: string;
  form?: string;
}

function errorsFor(err: unknown): FormErrors {
  if (err instanceof AppError) {
    switch (err.code) {
      case "type_key_taken":
        return { key: "This key is already used by another type." };
      case "invalid_icon":
        return { icon: "Choose a valid icon." };
      case "invalid_request":
        return { label: "Enter a label for the default language." };
    }
  }
  return { form: "Couldn't save this type. Try again." };
}

function emptyState(defaultLocale: string) {
  return {
    label: {} as Record<string, string>,
    defaultSummary: {} as Record<string, string>,
    icon: "",
    defaultDueDays: "",
    key: "",
    keyTouched: false,
    defaultLocale,
  };
}

// shell-ux.md §5.10 "Type sheet".
export function ActivityTypeSheet({ open, onClose, editing }: ActivityTypeSheetProps): ReactNode {
  const tenant = useTenant();
  const otherLocales = tenant.availableLocales.filter((tag) => tag !== tenant.defaultLocale);
  const create = useCreateActivityType();
  const update = useUpdateActivityType();
  const mutation = editing ? update : create;
  const [draft, setDraft] = useState(() => emptyState(tenant.defaultLocale));
  const [errors, setErrors] = useState<FormErrors>({});

  // biome-ignore lint/correctness/useExhaustiveDependencies: resets only when the sheet opens or the target type changes.
  useEffect(() => {
    if (!open) return;
    setErrors({});
    if (editing) {
      setDraft({
        label: editing.label,
        defaultSummary: editing.defaultSummary,
        icon: editing.icon,
        defaultDueDays: editing.defaultDueDays === null ? "" : String(editing.defaultDueDays),
        key: editing.key,
        keyTouched: true,
        defaultLocale: tenant.defaultLocale,
      });
    } else {
      setDraft(emptyState(tenant.defaultLocale));
    }
  }, [open, editing]);

  function setLabel(label: Record<string, string>): void {
    setDraft((current) => ({
      ...current,
      label,
      key: !current.keyTouched && !editing ? slugifyActivityTypeKey(label[current.defaultLocale] ?? "") : current.key,
    }));
    setErrors(({ label: _label, ...rest }) => rest);
  }

  async function submit(event?: SubmitEvent<HTMLFormElement>): Promise<void> {
    event?.preventDefault();
    const label = draft.label[tenant.defaultLocale]?.trim();
    if (!label) {
      setErrors({ label: "Enter a label for the default language." });
      return;
    }
    if (!draft.icon) {
      setErrors({ icon: "Choose an icon." });
      return;
    }
    if (!editing && !/^[a-z][a-z0-9_]{0,39}$/.test(draft.key)) {
      setErrors({ key: "Use lowercase letters, numbers and underscores, starting with a letter." });
      return;
    }
    const dueDays = draft.defaultDueDays.trim() === "" ? undefined : Number(draft.defaultDueDays);
    setErrors({});
    try {
      if (editing) {
        await update.mutateAsync({
          key: editing.key,
          changes: {
            label: draft.label,
            icon: draft.icon,
            defaultSummary: draft.defaultSummary,
            defaultDueDays: dueDays ?? null,
          },
        });
      } else {
        await create.mutateAsync({
          key: draft.key,
          label: draft.label,
          icon: draft.icon,
          ...(Object.keys(draft.defaultSummary).length > 0 ? { defaultSummary: draft.defaultSummary } : {}),
          ...(dueDays !== undefined ? { defaultDueDays: dueDays } : {}),
        });
      }
      onClose();
    } catch (err) {
      setErrors(errorsFor(err));
    }
  }

  return (
    <SideSheet open={open} onClose={onClose} title={editing ? "Edit type" : "Add type"}>
      <form className="flex flex-col gap-4 p-4" onSubmit={(event) => void submit(event)} noValidate>
        <LocaleFieldSet
          label="Label"
          value={draft.label}
          onChange={setLabel}
          defaultLocale={tenant.defaultLocale}
          otherLocales={otherLocales}
          required
          error={errors.label}
          maxLength={60}
        />
        {editing ? (
          <FieldWrapper label="Key" description="Used in the API and exports. It can't be changed later.">
            <TextInput value={draft.key} onChange={() => {}} disabled />
          </FieldWrapper>
        ) : (
          <FieldWrapper
            label="Key"
            required
            error={errors.key}
            description="Used in the API and exports. It can't be changed later."
          >
            <TextInput
              value={draft.key}
              onChange={(value) => setDraft((current) => ({ ...current, key: value, keyTouched: true }))}
              maxLength={40}
            />
          </FieldWrapper>
        )}
        <FieldWrapper label="Icon" required error={errors.icon}>
          <IconPicker
            value={draft.icon || undefined}
            onChange={(icon) => {
              setDraft((current) => ({ ...current, icon }));
              setErrors(({ icon: _icon, ...rest }) => rest);
            }}
            placeholder="Choose an icon…"
          />
        </FieldWrapper>
        <LocaleFieldSet
          label="Default summary"
          value={draft.defaultSummary}
          onChange={(defaultSummary) => setDraft((current) => ({ ...current, defaultSummary }))}
          defaultLocale={tenant.defaultLocale}
          otherLocales={otherLocales}
          maxLength={200}
        />
        <FieldWrapper
          label="Due in (days)"
          error={errors.defaultDueDays}
          description="0 means due the day it's scheduled. Leave empty for no default."
        >
          <TextInput
            type="number"
            min={0}
            max={365}
            value={draft.defaultDueDays}
            onChange={(value) => setDraft((current) => ({ ...current, defaultDueDays: value }))}
          />
        </FieldWrapper>
        {errors.form && (
          <p role="alert" className="text-danger text-sm">
            {errors.form}
          </p>
        )}
        <div>
          <ActionButton variant="primary" loading={mutation.isPending} onClick={() => void submit()}>
            {editing ? "Save" : "Add type"}
          </ActionButton>
        </div>
      </form>
    </SideSheet>
  );
}
