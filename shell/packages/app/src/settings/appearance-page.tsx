import { type DateFormat, type UpdatePreferencesInput, useAuth, useTenant, useUser } from "@goerp/sdk/auth";
import { FieldWrapper, PageHeader, PageLayout, SegmentedField, Select, TimezoneSelect } from "@goerp/sdk/components";
import { localeStore, useLocale } from "@goerp/sdk/i18n";
import { toast } from "@goerp/sdk/notifications";
import { type ThemePreference, useTheme } from "@goerp/sdk/react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { formatDateAs, nativeLocaleName, timezonePreview } from "./appearance-format.js";

// Select can't use "" as an option value (Radix), so these stand for null.
const INHERIT = "inherit";

const THEME_OPTIONS = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
];

const DATE_FORMATS: DateFormat[] = ["day_first", "month_first", "iso"];

export interface AppearancePageProps {
  // Called after a language change saves, since the locale also affects
  // server-rendered data (shell-ux.md §4.4). Stories and tests replace it.
  reload?: () => void;
}

// shell-ux.md §4.4: every control saves on change, applied optimistically
// and reverted with a toast if the save fails.
export function AppearancePage({ reload = () => window.location.reload() }: AppearancePageProps): ReactNode {
  const user = useUser();
  const tenant = useTenant();
  const { updatePreferences } = useAuth();
  const { preference, setPreference } = useTheme();
  const { locale: uiLocale } = useLocale();

  const [locale, setLocale] = useState(user.locale ?? INHERIT);
  const [timezone, setTimezone] = useState(user.timezone ?? "");
  const [dateFormat, setDateFormat] = useState<string>(user.dateFormat ?? INHERIT);
  const now = useNow();
  // Per control, the latest save: an earlier save that settles after a
  // later change neither reverts nor acts on that change.
  const latestSave = useRef<Record<string, number>>({});

  // Resolves true when the save succeeded and is still the control's latest.
  async function save(input: UpdatePreferencesInput, what: string, revert: () => void): Promise<boolean> {
    const token = (latestSave.current[what] ?? 0) + 1;
    latestSave.current[what] = token;
    try {
      await updatePreferences(input);
      return latestSave.current[what] === token;
    } catch {
      if (latestSave.current[what] === token) {
        revert();
        toast.error(`Couldn't save your ${what}. Try again.`);
      }
      return false;
    }
  }

  function changeTheme(next: ThemePreference) {
    const previous = preference;
    setPreference(next);
    void save({ theme: next }, "theme", () => setPreference(previous));
  }

  async function changeLocale(next: string) {
    const previous = locale;
    setLocale(next);
    const value = next === INHERIT ? null : next;
    if (await save({ locale: value }, "language", () => setLocale(previous))) {
      localeStore.setLocale(value ?? tenant.defaultLocale);
      reload();
    }
  }

  function changeTimezone(next: string) {
    const previous = timezone;
    setTimezone(next);
    void save({ timezone: next === "" ? null : next }, "timezone", () => setTimezone(previous));
  }

  function changeDateFormat(next: string) {
    const previous = dateFormat;
    setDateFormat(next);
    void save({ dateFormat: next === INHERIT ? null : (next as DateFormat) }, "date format", () =>
      setDateFormat(previous),
    );
  }

  const localeOptions = [
    { value: INHERIT, label: `Organisation default (${nativeLocaleName(tenant.defaultLocale)})` },
    ...tenant.availableLocales.map((code) => ({ value: code, label: nativeLocaleName(code) })),
  ];
  const dateFormatOptions = [
    { value: INHERIT, label: "Automatic (from language)" },
    ...DATE_FORMATS.map((format) => ({ value: format, label: formatDateAs(now, format, uiLocale) })),
  ];
  const preview = timezonePreview(timezone || tenant.defaultTimezone, now, uiLocale);

  return (
    <PageLayout>
      <PageHeader title="Appearance" subtitle="Choose your theme, language, timezone, and date format." />
      <div className="flex max-w-md flex-col gap-6">
        <FieldWrapper label="Theme">
          <SegmentedField
            options={THEME_OPTIONS}
            value={preference}
            onChange={(next) => changeTheme(next as ThemePreference)}
          />
        </FieldWrapper>
        <FieldWrapper label="Language">
          <Select
            options={localeOptions}
            value={locale}
            emptyValue={INHERIT}
            onChange={(next) => void changeLocale(next as string)}
          />
        </FieldWrapper>
        <FieldWrapper label="Timezone">
          <TimezoneSelect
            value={timezone}
            emptyLabel={`Organisation default (${tenant.defaultTimezone})`}
            onChange={changeTimezone}
          />
          {preview && <p className="mt-1 text-sm text-text-secondary">{preview}</p>}
        </FieldWrapper>
        <FieldWrapper label="Date format">
          <Select
            options={dateFormatOptions}
            value={dateFormat}
            emptyValue={INHERIT}
            onChange={(next) => changeDateFormat(next as string)}
          />
        </FieldWrapper>
      </div>
    </PageLayout>
  );
}

// The current time, refreshed every 30 seconds for the timezone preview.
function useNow(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  return now;
}
