import { Checkbox, FieldWrapper, SegmentedField, Select } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import { errorFor, type FieldError, localeLabel, reportSaveError, SettingsSection } from "./settings-section.js";
import {
  type FirstDayOfWeek,
  type LocalisationSettings,
  type TenantSettingsPatch,
  useUpdateTenantSettings,
} from "./tenant-settings-api.js";

const FIELDS = ["localisation"] as const;

const FIRST_DAYS: { value: FirstDayOfWeek; label: string }[] = [
  { value: "monday", label: "Monday" },
  { value: "sunday", label: "Sunday" },
];

const NUMBER_FORMATS = ["1,234.56", "1.234,56", "1 234,56"].map((f) => ({ value: f, label: f }));

type LocalisationDraft = Omit<LocalisationSettings, "platformLocales">;

export interface LocalisationSectionProps {
  saved: LocalisationSettings;
  defaultLocale: string;
}

export function LocalisationSection({ saved, defaultLocale }: LocalisationSectionProps): ReactNode {
  const { platformLocales, ...initial } = saved;
  const [draft, setDraft] = useState<LocalisationDraft>(initial);
  const [error, setError] = useState<FieldError | null>(null);
  const update = useUpdateTenantSettings();

  const set = <K extends keyof LocalisationDraft>(key: K, value: LocalisationDraft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setError(null);
  };

  const toggleLocale = (tag: string, on: boolean) =>
    set(
      "availableLocales",
      // Kept in the platform's order, so the saved list doesn't depend on click order.
      platformLocales.filter((t) => (t === tag ? on : draft.availableLocales.includes(t))),
    );

  const changes: NonNullable<TenantSettingsPatch["localisation"]> = {};
  if (draft.availableLocales.join(",") !== initial.availableLocales.join(",")) {
    changes.availableLocales = draft.availableLocales;
  }
  if (draft.firstDayOfWeek !== initial.firstDayOfWeek) changes.firstDayOfWeek = draft.firstDayOfWeek;
  if (draft.numberFormat !== initial.numberFormat) changes.numberFormat = draft.numberFormat;
  const dirty = Object.keys(changes).length > 0;

  const save = () => {
    if (draft.availableLocales.length === 0) {
      setError({ field: "localisation.available_locales", message: "Choose at least one language." });
      return;
    }
    update.mutate(
      { localisation: changes },
      {
        onSuccess: ({ localisation: { platformLocales: _, ...next } }) => {
          setDraft(next);
          toast.success("Localisation settings saved.");
        },
        onError: (err) => setError(reportSaveError(err, FIELDS)),
      },
    );
  };

  return (
    <SettingsSection title="Localisation" dirty={dirty} saving={update.isPending} onSave={save}>
      <FieldWrapper
        label="Available languages"
        description="The languages members can choose in their appearance settings. The default language stays available."
        error={errorFor(error, "localisation.available_locales")}
      >
        <div className="flex flex-col gap-2">
          {platformLocales.map((tag) => (
            <Checkbox
              key={tag}
              label={localeLabel(tag)}
              checked={draft.availableLocales.includes(tag)}
              disabled={tag === defaultLocale}
              onChange={(on) => toggleLocale(tag, on)}
            />
          ))}
        </div>
      </FieldWrapper>
      <FieldWrapper
        label="First day of the week"
        description="Used by calendar views."
        error={errorFor(error, "localisation.first_day_of_week")}
      >
        <SegmentedField
          options={FIRST_DAYS}
          value={draft.firstDayOfWeek}
          onChange={(v) => set("firstDayOfWeek", v as FirstDayOfWeek)}
        />
      </FieldWrapper>
      <FieldWrapper label="Number format" required error={errorFor(error, "localisation.number_format")}>
        <Select
          options={NUMBER_FORMATS}
          value={draft.numberFormat}
          onChange={(v) => set("numberFormat", v as string)}
        />
      </FieldWrapper>
    </SettingsSection>
  );
}
