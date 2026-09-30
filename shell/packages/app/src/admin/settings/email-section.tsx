import { Button, FieldWrapper, SegmentedField, TextInput, ToggleField } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import {
  errorFor,
  type FieldError,
  fieldErrorOf,
  parseWhole,
  reportSaveError,
  SettingsSection,
} from "./settings-section.js";
import {
  type EmailProvider,
  type EmailSettings,
  type EmailSettingsPatch,
  SECRET_MASK,
  useSendTestEmail,
  useUpdateNotificationDelivery,
} from "./tenant-settings-api.js";

const FIELDS = ["email"] as const;

const PROVIDERS: { value: EmailProvider; label: string }[] = [
  { value: "resend", label: "Resend" },
  { value: "smtp", label: "SMTP" },
];

const LOCKED_NOTE = "Set by your platform operator.";

// Secrets are write-only: apiKey and password hold a newly typed value, and
// "" leaves the stored one as it is.
interface EmailDraft {
  provider: EmailProvider;
  apiKey: string;
  fromName: string;
  fromAddr: string;
  replyTo: string;
  host: string;
  port: string;
  user: string;
  password: string;
  useTLS: boolean;
}

function draftOf(e: EmailSettings): EmailDraft {
  return {
    provider: e.provider,
    apiKey: "",
    fromName: e.fromName,
    fromAddr: e.fromAddr,
    replyTo: e.replyTo,
    host: e.smtp.host,
    port: String(e.smtp.port),
    user: e.smtp.user,
    password: "",
    useTLS: e.smtp.useTLS,
  };
}

function toPatch(draft: EmailDraft, saved: EmailDraft, all: boolean): EmailSettingsPatch | FieldError {
  // The port is checked only while SMTP is selected: under Resend its
  // field is hidden, so an error there would block the form invisibly.
  const port = parseWhole(draft.port);
  if (port === null && draft.provider === "smtp") return { field: "email.smtp.port", message: "Enter a port number." };
  // A test email sends the whole form, less the fields left empty and
  // unchanged, so the engine names a missing setting as missing.
  const changed = <K extends keyof EmailDraft>(key: K) => draft[key] !== saved[key] || (all && draft[key] !== "");

  const patch: EmailSettingsPatch = {};
  if (changed("provider")) patch.provider = draft.provider;
  if (draft.apiKey !== "") patch.apiKey = draft.apiKey;
  if (changed("fromName")) patch.fromName = draft.fromName;
  if (changed("fromAddr")) patch.fromAddr = draft.fromAddr;
  if (changed("replyTo")) patch.replyTo = draft.replyTo;

  const smtp: NonNullable<EmailSettingsPatch["smtp"]> = {};
  if (changed("host")) smtp.host = draft.host;
  if (port !== null && changed("port")) smtp.port = port;
  if (changed("user")) smtp.user = draft.user;
  if (draft.password !== "") smtp.password = draft.password;
  if (changed("useTLS")) smtp.useTLS = draft.useTLS;
  if (Object.keys(smtp).length > 0) patch.smtp = smtp;
  return patch;
}

function isFieldError(v: EmailSettingsPatch | FieldError): v is FieldError {
  return "field" in v && "message" in v;
}

export interface EmailSectionProps {
  saved: EmailSettings;
  locked: string[];
}

// shell-ux.md §5.5 "Email": the provider notification emails are sent
// through. "Send test email" uses the form as it is, saved or not.
export function EmailSection({ saved, locked }: EmailSectionProps): ReactNode {
  const initial = draftOf(saved);
  const [draft, setDraft] = useState<EmailDraft>(initial);
  const [error, setError] = useState<FieldError | null>(null);
  const update = useUpdateNotificationDelivery();
  const testEmail = useSendTestEmail();

  const set = <K extends keyof EmailDraft>(key: K, value: EmailDraft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setError(null);
  };
  const isLocked = (field: string) => locked.includes(field);
  const describe = (field: string, description?: string) => (isLocked(field) ? LOCKED_NOTE : description);

  const dirty = (Object.keys(draft) as (keyof EmailDraft)[]).some((key) => draft[key] !== initial[key]);

  const save = () => {
    const patch = toPatch(draft, initial, false);
    if (isFieldError(patch)) {
      setError(patch);
      return;
    }
    update.mutate(
      { email: patch },
      {
        onSuccess: (delivery) => {
          setDraft(draftOf(delivery.email));
          toast.success("Email settings saved.");
        },
        onError: (err) => setError(reportSaveError(err, FIELDS)),
      },
    );
  };

  const sendTest = () => {
    const patch = toPatch(draft, initial, true);
    if (isFieldError(patch)) {
      setError(patch);
      return;
    }
    testEmail.mutate(patch, {
      onSuccess: (result) => toast.success(`Test email sent to ${result.sentTo}.`),
      onError: (err) => {
        const fe = fieldErrorOf(err);
        if (fe) {
          setError(fe);
          return;
        }
        if (err instanceof AppError && err.httpStatus === 429) {
          toast.error("You've sent several test emails recently. Try again in a few minutes.");
          return;
        }
        const reason = err instanceof AppError && typeof err.details?.message === "string" ? err.details.message : null;
        toast.error(reason ? `The test email wasn't sent: ${reason}` : "Couldn't send the test email. Try again.");
      },
    });
  };

  const apiKeyStored = saved.apiKey === SECRET_MASK;
  const passwordStored = saved.smtp.password === SECRET_MASK;

  return (
    <SettingsSection
      title="Email"
      description="The provider and sender used for notification emails."
      dirty={dirty}
      saving={update.isPending}
      onSave={save}
      actions={
        <Button variant="secondary" onClick={sendTest} disabled={testEmail.isPending}>
          {testEmail.isPending ? "Sending…" : "Send test email"}
        </Button>
      }
    >
      <FieldWrapper label="Provider" description={describe("email.provider")} error={errorFor(error, "email.provider")}>
        <SegmentedField
          options={PROVIDERS}
          value={draft.provider}
          onChange={(v) => set("provider", v as EmailProvider)}
          disabled={isLocked("email.provider")}
        />
      </FieldWrapper>

      {draft.provider === "resend" ? (
        <FieldWrapper
          label="Resend API key"
          description={describe(
            "email.api_key",
            apiKeyStored ? "A key is saved. Enter a new one to replace it." : undefined,
          )}
          error={errorFor(error, "email.api_key")}
        >
          <TextInput
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={draft.apiKey}
            onChange={(v) => set("apiKey", v)}
            placeholder={apiKeyStored ? "••••••••••••" : "re_…"}
            disabled={isLocked("email.api_key")}
          />
        </FieldWrapper>
      ) : (
        <>
          <FieldWrapper
            label="SMTP host"
            description={describe("email.smtp.host")}
            error={errorFor(error, "email.smtp.host")}
          >
            <TextInput
              value={draft.host}
              onChange={(v) => set("host", v)}
              placeholder="smtp.example.com"
              disabled={isLocked("email.smtp.host")}
            />
          </FieldWrapper>
          <FieldWrapper
            label="Port"
            description={describe("email.smtp.port")}
            error={errorFor(error, "email.smtp.port")}
          >
            <TextInput
              type="number"
              inputMode="numeric"
              min={1}
              max={65535}
              value={draft.port}
              onChange={(v) => set("port", v)}
              disabled={isLocked("email.smtp.port")}
            />
          </FieldWrapper>
          <FieldWrapper
            label="Username"
            description={describe("email.smtp.user")}
            error={errorFor(error, "email.smtp.user")}
          >
            <TextInput
              autoComplete="off"
              value={draft.user}
              onChange={(v) => set("user", v)}
              disabled={isLocked("email.smtp.user")}
            />
          </FieldWrapper>
          <FieldWrapper
            label="Password"
            description={describe(
              "email.smtp.password",
              passwordStored ? "A password is saved. Enter a new one to replace it." : undefined,
            )}
            error={errorFor(error, "email.smtp.password")}
          >
            <TextInput
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={draft.password}
              onChange={(v) => set("password", v)}
              placeholder={passwordStored ? "••••••••••••" : undefined}
              disabled={isLocked("email.smtp.password")}
            />
          </FieldWrapper>
          <FieldWrapper
            label="Use TLS"
            description={describe("email.smtp.use_tls", "Encrypts the connection with STARTTLS.")}
            error={errorFor(error, "email.smtp.use_tls")}
          >
            <ToggleField
              value={draft.useTLS}
              onChange={(v) => set("useTLS", v)}
              disabled={isLocked("email.smtp.use_tls")}
            />
          </FieldWrapper>
        </>
      )}

      <FieldWrapper
        label="Sender name"
        description={describe("email.from_name", "Leave empty to use your company name.")}
        error={errorFor(error, "email.from_name")}
      >
        <TextInput value={draft.fromName} onChange={(v) => set("fromName", v)} disabled={isLocked("email.from_name")} />
      </FieldWrapper>
      <FieldWrapper
        label="From address"
        description={describe("email.from_addr")}
        error={errorFor(error, "email.from_addr")}
      >
        <TextInput
          type="email"
          value={draft.fromAddr}
          onChange={(v) => set("fromAddr", v)}
          placeholder="noreply@example.com"
          disabled={isLocked("email.from_addr")}
        />
      </FieldWrapper>
      <FieldWrapper
        label="Reply-to address"
        description={describe("email.reply_to", "Optional. Where replies to notification emails go.")}
        error={errorFor(error, "email.reply_to")}
      >
        <TextInput
          type="email"
          value={draft.replyTo}
          onChange={(v) => set("replyTo", v)}
          disabled={isLocked("email.reply_to")}
        />
      </FieldWrapper>
    </SettingsSection>
  );
}
