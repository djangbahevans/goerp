import { FieldWrapper, SegmentedField, Select, TextArea, TextInput, ToggleField } from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import { useAdminRoles } from "../roles/admin-roles-api.js";
import { errorFor, type FieldError, parseWhole, reportSaveError, SettingsSection } from "./settings-section.js";
import {
  type MFAMode,
  type PasswordEnforcement,
  type SecuritySettings,
  type TenantSettingsPatch,
  useUpdateTenantSettings,
} from "./tenant-settings-api.js";

const FIELDS = ["security"] as const;

const MFA_MODES: { value: MFAMode; label: string }[] = [
  { value: "optional", label: "Optional" },
  { value: "required", label: "Required for all users" },
  { value: "required_for_roles", label: "Required for specific roles" },
];

const ENFORCEMENTS: { value: PasswordEnforcement; label: string }[] = [
  { value: "nudge", label: "Ask to change it" },
  { value: "require", label: "Require a change" },
];

// Number fields are edited as text and parsed on save.
interface SecurityDraft {
  mfaMode: MFAMode;
  requiredRoles: string[];
  maxAssuranceAgeHours: string;
  minLength: string;
  enforcement: PasswordEnforcement;
  graceDays: string;
  idleTimeoutMinutes: string;
  absoluteMaxMinutes: string;
  ipAllowlist: string;
  requireVerification: boolean;
}

function draftOf(s: SecuritySettings): SecurityDraft {
  return {
    mfaMode: s.mfa.mode,
    requiredRoles: s.mfa.requiredRoles,
    maxAssuranceAgeHours: String(s.mfa.maxAssuranceAgeHours),
    minLength: String(s.passwordPolicy.minLength),
    enforcement: s.passwordPolicy.enforcement,
    graceDays: String(s.passwordPolicy.graceDays),
    idleTimeoutMinutes: String(s.session.idleTimeoutMinutes),
    absoluteMaxMinutes: String(s.session.absoluteMaxMinutes),
    ipAllowlist: s.ipAllowlist,
    requireVerification: s.emailVerification.required,
  };
}

function sameList(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

type Parsed = { patch: NonNullable<TenantSettingsPatch["security"]> } | { error: FieldError };

// The PATCH for every group that changed, each group sent whole.
function toPatch(draft: SecurityDraft, saved: SecurityDraft): Parsed {
  const whole = (field: string, value: string): number | FieldError =>
    parseWhole(value) ?? { field, message: "Enter a whole number." };
  const patch: NonNullable<TenantSettingsPatch["security"]> = {};

  if (
    draft.mfaMode !== saved.mfaMode ||
    !sameList(draft.requiredRoles, saved.requiredRoles) ||
    draft.maxAssuranceAgeHours !== saved.maxAssuranceAgeHours
  ) {
    const hours = whole("security.mfa.max_assurance_age_hours", draft.maxAssuranceAgeHours);
    if (typeof hours !== "number") return { error: hours };
    patch.mfa = {
      mode: draft.mfaMode,
      requiredRoles: draft.mfaMode === "required_for_roles" ? draft.requiredRoles : [],
      maxAssuranceAgeHours: hours,
    };
  }
  if (
    draft.minLength !== saved.minLength ||
    draft.enforcement !== saved.enforcement ||
    draft.graceDays !== saved.graceDays
  ) {
    const minLength = whole("security.password_policy.min_length", draft.minLength);
    if (typeof minLength !== "number") return { error: minLength };
    const graceDays = whole("security.password_policy.grace_days", draft.graceDays);
    if (typeof graceDays !== "number") return { error: graceDays };
    patch.passwordPolicy = { minLength, enforcement: draft.enforcement, graceDays };
  }
  if (draft.idleTimeoutMinutes !== saved.idleTimeoutMinutes || draft.absoluteMaxMinutes !== saved.absoluteMaxMinutes) {
    const idle = whole("security.session", draft.idleTimeoutMinutes);
    if (typeof idle !== "number") return { error: idle };
    const absolute = whole("security.session", draft.absoluteMaxMinutes);
    if (typeof absolute !== "number") return { error: absolute };
    patch.session = { idleTimeoutMinutes: idle, absoluteMaxMinutes: absolute };
  }
  if (draft.ipAllowlist !== saved.ipAllowlist) patch.ipAllowlist = draft.ipAllowlist;
  if (draft.requireVerification !== saved.requireVerification) {
    patch.emailVerification = { required: draft.requireVerification };
  }
  return { patch };
}

export function SecuritySection({ saved }: { saved: SecuritySettings }): ReactNode {
  const initial = draftOf(saved);
  const [draft, setDraft] = useState<SecurityDraft>(initial);
  const [error, setError] = useState<FieldError | null>(null);
  const update = useUpdateTenantSettings();
  const roles = useAdminRoles();

  const set = <K extends keyof SecurityDraft>(key: K, value: SecurityDraft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setError(null);
  };

  const dirty = (Object.keys(draft) as (keyof SecurityDraft)[]).some((key) => {
    const a = draft[key];
    const b = initial[key];
    return Array.isArray(a) && Array.isArray(b) ? !sameList(a, b) : a !== b;
  });

  const save = () => {
    const parsed = toPatch(draft, initial);
    if ("error" in parsed) {
      setError(parsed.error);
      return;
    }
    update.mutate(
      { security: parsed.patch },
      {
        onSuccess: (settings) => {
          setDraft(draftOf(settings.security));
          toast.success("Security settings saved.");
        },
        onError: (err) => setError(reportSaveError(err, FIELDS)),
      },
    );
  };

  const verification = saved.emailVerification;

  return (
    <SettingsSection title="Security" dirty={dirty} saving={update.isPending} onSave={save}>
      <FieldWrapper label="Two-factor authentication" required error={errorFor(error, "security.mfa.mode")}>
        <Select options={MFA_MODES} value={draft.mfaMode} onChange={(v) => set("mfaMode", v as MFAMode)} />
      </FieldWrapper>
      {draft.mfaMode === "required_for_roles" && (
        <FieldWrapper label="Roles that must use two-factor" error={errorFor(error, "security.mfa.required_roles")}>
          <Select
            multiple
            options={(roles.data ?? []).map((r) => ({ value: r.name, label: r.name }))}
            value={draft.requiredRoles}
            onChange={(v) => set("requiredRoles", v as string[])}
            placeholder={roles.isLoading ? "Loading roles…" : "Choose roles"}
          />
        </FieldWrapper>
      )}
      <FieldWrapper
        label="Ask for two-factor again after (hours)"
        description="While two-factor is required, members confirm a code again once this long has passed since their last check. 1 to 720."
        error={errorFor(error, "security.mfa.max_assurance_age_hours")}
      >
        <TextInput
          type="number"
          inputMode="numeric"
          value={draft.maxAssuranceAgeHours}
          onChange={(v) => set("maxAssuranceAgeHours", v)}
        />
      </FieldWrapper>

      <FieldWrapper
        label="Minimum password length"
        description="Longer passwords are harder to guess than ones with required symbols. Require two-factor authentication for stronger protection."
        error={errorFor(error, "security.password_policy.min_length")}
      >
        <TextInput
          type="number"
          inputMode="numeric"
          min={12}
          max={20}
          value={draft.minLength}
          onChange={(v) => set("minLength", v)}
        />
      </FieldWrapper>
      <FieldWrapper
        label="When a password is too short"
        error={errorFor(error, "security.password_policy.enforcement")}
      >
        <SegmentedField
          options={ENFORCEMENTS}
          value={draft.enforcement}
          onChange={(v) => set("enforcement", v as PasswordEnforcement)}
        />
      </FieldWrapper>
      {draft.enforcement === "require" && (
        <FieldWrapper
          label="Grace period (days)"
          description="Existing members get this many days from when you save this setting. People who join afterwards change a short password the first time they sign in. 0 to 90."
          error={errorFor(error, "security.password_policy.grace_days")}
        >
          <TextInput
            type="number"
            inputMode="numeric"
            min={0}
            max={90}
            value={draft.graceDays}
            onChange={(v) => set("graceDays", v)}
          />
        </FieldWrapper>
      )}

      <div className="flex flex-col gap-3">
        <FieldWrapper
          label="Sign out after inactivity (minutes)"
          description="15 to 43,200, or 0 to never sign out an idle session."
          error={errorFor(error, "security.session")}
        >
          <TextInput
            type="number"
            inputMode="numeric"
            min={0}
            value={draft.idleTimeoutMinutes}
            onChange={(v) => set("idleTimeoutMinutes", v)}
          />
        </FieldWrapper>
        <FieldWrapper
          label="Maximum session length (minutes)"
          description="60 to 525,600, and no shorter than the inactivity timeout, or 0 for no limit."
        >
          <TextInput
            type="number"
            inputMode="numeric"
            min={0}
            value={draft.absoluteMaxMinutes}
            onChange={(v) => set("absoluteMaxMinutes", v)}
          />
        </FieldWrapper>
      </div>

      <FieldWrapper
        label="Require email verification on registration"
        description={
          verification.policy === "required"
            ? "Required by your platform configuration."
            : verification.policy === "off"
              ? "Disabled by your platform configuration."
              : "This setting doesn't affect anything yet."
        }
        error={errorFor(error, "security.email_verification")}
      >
        <ToggleField
          value={draft.requireVerification}
          onChange={(v) => set("requireVerification", v)}
          disabled={verification.policy !== "tenant_choice"}
        />
      </FieldWrapper>

      <FieldWrapper
        label="Allowed sign-in addresses"
        description="IP addresses or CIDR ranges, separated by commas, such as 203.0.113.0/24. Leave empty to allow sign-in from anywhere."
        error={errorFor(error, "security.ip_allowlist")}
      >
        <TextArea value={draft.ipAllowlist} onChange={(v) => set("ipAllowlist", v)} rows={2} />
      </FieldWrapper>
    </SettingsSection>
  );
}
