import {
  Button,
  CountrySelect,
  CurrencySelect,
  FieldWrapper,
  Select,
  TextArea,
  TextInput,
  TimezoneSelect,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useRef, useState } from "react";
import { errorFor, type FieldError, localeLabel, reportSaveError, SettingsSection } from "./settings-section.js";
import { normalizeTaxID, TAX_ID_MAX_LENGTH } from "./tax-id.js";
import {
  type GeneralSettings,
  type TenantSettingsPatch,
  useRemoveLogo,
  useUpdateTenantSettings,
  useUploadLogo,
} from "./tenant-settings-api.js";

type GeneralDraft = Omit<GeneralSettings, "logoUrl">;

function draftOf({ logoUrl: _, ...draft }: GeneralSettings): GeneralDraft {
  return draft;
}

const FIELDS = ["general"] as const;

export interface GeneralSectionProps {
  saved: GeneralSettings;
  availableLocales: string[];
}

export function GeneralSection({ saved, availableLocales }: GeneralSectionProps): ReactNode {
  const initial = draftOf(saved);
  const [draft, setDraft] = useState<GeneralDraft>(initial);
  const [error, setError] = useState<FieldError | null>(null);
  const update = useUpdateTenantSettings();

  const set = <K extends keyof GeneralDraft>(key: K, value: GeneralDraft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setError(null);
  };

  const changes: NonNullable<TenantSettingsPatch["general"]> = {};
  for (const key of Object.keys(draft) as (keyof GeneralDraft)[]) {
    if (draft[key] !== initial[key]) changes[key] = draft[key];
  }
  const dirty = Object.keys(changes).length > 0;

  const save = () => {
    if (draft.name.trim() === "") {
      setError({ field: "general.name", message: "Enter a company name." });
      return;
    }
    if ([...normalizeTaxID(draft.taxId)].length > TAX_ID_MAX_LENGTH) {
      setError({ field: "general.tax_id", message: "Enter a tax ID of 100 characters or fewer." });
      return;
    }
    update.mutate(
      { general: changes },
      {
        // The server trims text, so a save that only added spaces returns the
        // same settings and wouldn't reset the form by itself.
        onSuccess: (settings) => {
          setDraft(draftOf(settings.general));
          toast.success("General settings saved.");
        },
        onError: (err) => setError(reportSaveError(err, FIELDS)),
      },
    );
  };

  return (
    <SettingsSection title="General" dirty={dirty} saving={update.isPending} onSave={save}>
      <LogoControl logoUrl={saved.logoUrl} />
      <FieldWrapper label="Company name" required error={errorFor(error, "general.name")}>
        <TextInput value={draft.name} onChange={(v) => set("name", v)} autoComplete="organization" />
      </FieldWrapper>
      <FieldWrapper label="Address" error={errorFor(error, "general.address")}>
        <TextArea value={draft.address} onChange={(v) => set("address", v)} rows={3} />
      </FieldWrapper>
      <FieldWrapper label="Tax ID" error={errorFor(error, "general.tax_id")}>
        <TextInput value={draft.taxId} onChange={(v) => set("taxId", v)} />
      </FieldWrapper>
      <FieldWrapper label="Website" error={errorFor(error, "general.website")}>
        <TextInput
          type="url"
          value={draft.website}
          onChange={(v) => set("website", v)}
          placeholder="https://example.com"
        />
      </FieldWrapper>
      <FieldWrapper
        label="Country"
        description="Decides which country-specific modules apply to your company."
        error={errorFor(error, "general.country")}
      >
        <CountrySelect value={draft.country || undefined} onChange={(v) => set("country", v)} />
      </FieldWrapper>
      <FieldWrapper label="Default currency" error={errorFor(error, "general.default_currency")}>
        <CurrencySelect value={draft.defaultCurrency || undefined} onChange={(v) => set("defaultCurrency", v)} />
      </FieldWrapper>
      <FieldWrapper
        label="Default language"
        required
        description="For members who haven't chosen their own."
        error={errorFor(error, "general.default_locale")}
      >
        <Select
          options={availableLocales.map((tag) => ({ value: tag, label: localeLabel(tag) }))}
          value={draft.defaultLocale}
          onChange={(v) => set("defaultLocale", v as string)}
        />
      </FieldWrapper>
      <FieldWrapper
        label="Default time zone"
        required
        description="For members who haven't chosen their own."
        error={errorFor(error, "general.default_timezone")}
      >
        <TimezoneSelect value={draft.defaultTimezone} onChange={(v) => set("defaultTimezone", v)} />
      </FieldWrapper>
    </SettingsSection>
  );
}

const LOGO_TYPES = "image/png,image/jpeg,image/gif,image/webp";

function logoErrorMessage(err: unknown): string {
  if (err instanceof AppError) {
    if (err.httpStatus === 413) return "The logo is larger than 2 MB.";
    if (err.httpStatus === 415) return "Use a PNG, JPEG, GIF or WebP image.";
    if (err.httpStatus === 503) return "Logo uploads aren't available right now.";
  }
  return "Couldn't update the logo. Try again.";
}

// The logo saves as soon as it's chosen or removed, apart from the
// section's Save.
function LogoControl({ logoUrl }: { logoUrl: string | null }): ReactNode {
  const input = useRef<HTMLInputElement>(null);
  const upload = useUploadLogo();
  const remove = useRemoveLogo();
  const busy = upload.isPending || remove.isPending;

  const onFile = (file: File | undefined) => {
    if (!file) return;
    upload.mutate(file, {
      onSuccess: () => toast.success("Logo updated."),
      onError: (err) => toast.error(logoErrorMessage(err)),
    });
  };

  return (
    <div className="flex flex-col gap-2">
      <span className="font-medium text-sm text-text">Company logo</span>
      <div className="flex items-center gap-4">
        <div className="flex h-16 w-32 items-center justify-center overflow-hidden rounded-control border border-border bg-bg-subtle">
          {logoUrl ? (
            <img src={logoUrl} alt="Company logo" className="max-h-full max-w-full object-contain" />
          ) : (
            <span className="text-sm text-text-secondary">No logo</span>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" size="sm" disabled={busy} onClick={() => input.current?.click()}>
            {logoUrl ? "Replace" : "Upload logo"}
          </Button>
          {logoUrl && (
            <Button
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={() =>
                remove.mutate(undefined, {
                  onSuccess: () => toast.success("Logo removed."),
                  onError: (err) => toast.error(logoErrorMessage(err)),
                })
              }
            >
              Remove
            </Button>
          )}
        </div>
        <input
          ref={input}
          type="file"
          accept={LOGO_TYPES}
          className="hidden"
          aria-label="Company logo file"
          onChange={(event) => {
            onFile(event.target.files?.[0]);
            event.target.value = "";
          }}
        />
      </div>
      <p className="text-sm text-text-secondary">PNG, JPEG, GIF or WebP, up to 2 MB.</p>
    </div>
  );
}
