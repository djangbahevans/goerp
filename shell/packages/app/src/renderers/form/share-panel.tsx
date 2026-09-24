import {
  ActionButton,
  Badge,
  DateField,
  FieldWrapper,
  Icon,
  SegmentedField,
  Skeleton,
  TextInput,
  UserAvatar,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import type { RecordShare, SharePermission } from "@goerp/sdk/react";
import { useShares } from "@goerp/sdk/react";
import { useEffect, useId, useRef, useState } from "react";

export interface SharePanelProps {
  resource: string;
  recordId: string;
  label: string;
  permissions: SharePermission[];
  headingId: string;
}

const PERMISSION_LABELS: Record<SharePermission, string> = { read: "Can view", write: "Can edit" };
const PERMISSION_VERBS: Record<SharePermission, string> = { read: "view", write: "edit" };
const EXPIRY_FORMAT = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

// DateField reads and writes date-only values as UTC midnight (date-fields.tsx).
function todayAsDateFieldValue(): Date {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return new Date(`${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`);
}

// The chosen day means through the end of that day in the user's local time.
function endOfLocalDay(picked: Date): string {
  return new Date(picked.getUTCFullYear(), picked.getUTCMonth(), picked.getUTCDate(), 23, 59, 59, 999).toISOString();
}

function ShareRow({
  share,
  isRevoking,
  error,
  onRevoke,
}: {
  share: RecordShare;
  isRevoking: boolean;
  error: string | undefined;
  onRevoke: () => void;
}) {
  const email = share.sharedWithEmail ?? "Unknown user";
  return (
    <li className={`flex flex-col gap-1 py-2 ${isRevoking ? "opacity-50" : ""}`}>
      <div className="flex items-center gap-2">
        <UserAvatar userId={share.sharedWithUserId} name={email} avatarUrl={null} size="sm" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm text-text" title={share.sharedWithEmail ?? share.sharedWithUserId}>
            {email}
          </div>
          <div className="mt-0.5 flex items-center gap-2">
            <Badge label={PERMISSION_LABELS[share.permission]} color={share.permission === "write" ? "blue" : "gray"} />
            {share.expiresAt && (
              <span className="text-text-secondary text-xs">
                Expires {EXPIRY_FORMAT.format(new Date(share.expiresAt))}
              </span>
            )}
          </div>
        </div>
        <ActionButton variant="ghost" size="sm" loading={isRevoking} onClick={onRevoke}>
          <span aria-hidden="true">Revoke</span>
          <span className="sr-only">Revoke access for {email}</span>
        </ActionButton>
      </div>
      {error && (
        <p role="alert" className="text-xs text-danger">
          Couldn't revoke access. {error}
        </p>
      )}
    </li>
  );
}

// docs/components/share-panel.md
export function SharePanel({ resource, recordId, label, permissions, headingId }: SharePanelProps) {
  const { shares, isLoading, isError, error, refetch, grant, isGranting, revoke, revokingIds } = useShares(
    resource,
    recordId,
  );
  const accessId = useId();
  const emailRef = useRef<HTMLInputElement | null>(null);
  const [email, setEmail] = useState("");
  const [permission, setPermission] = useState<SharePermission | undefined>(permissions[0]);
  const [expires, setExpires] = useState<Date | undefined>(undefined);
  const [emailError, setEmailError] = useState<string | undefined>(undefined);
  const [expiresError, setExpiresError] = useState<string | undefined>(undefined);
  const [formError, setFormError] = useState<string | undefined>(undefined);
  const [announcement, setAnnouncement] = useState("");
  const [revokeErrors, setRevokeErrors] = useState<Record<string, string>>({});

  // A disabled input can't take focus, so this refocuses on open and again once a request settles.
  useEffect(() => {
    if (!isGranting) emailRef.current?.focus();
  }, [isGranting]);

  const typed = email.trim().toLowerCase();
  const existingShare =
    typed === "" ? undefined : shares.find((share) => share.sharedWithEmail?.toLowerCase() === typed);

  async function submit(): Promise<void> {
    if (permission === undefined || isGranting) return;
    const recipient = email.trim();
    setEmailError(undefined);
    setExpiresError(undefined);
    setFormError(undefined);
    if (recipient === "") {
      setEmailError("Enter an email address.");
      emailRef.current?.focus();
      return;
    }
    if (expires && expires < todayAsDateFieldValue()) {
      setExpiresError("Choose today or a later date.");
      return;
    }
    const isUpdate = existingShare !== undefined;
    try {
      await grant({ userEmail: recipient, permission, expiresAt: expires ? endOfLocalDay(expires) : undefined });
      setAnnouncement(isUpdate ? `Updated access for ${recipient}.` : `Shared with ${recipient}.`);
      setEmail("");
      setExpires(undefined);
    } catch (err) {
      if (err instanceof AppError && err.code === "recipient_not_found") {
        setEmailError("No user with that email address.");
      } else {
        setFormError(err instanceof Error ? err.message : String(err));
      }
    }
  }

  async function handleRevoke(id: string): Promise<void> {
    setRevokeErrors(({ [id]: _cleared, ...rest }) => rest);
    try {
      await revoke(id);
    } catch (err) {
      setRevokeErrors((current) => ({ ...current, [id]: err instanceof Error ? err.message : String(err) }));
    }
  }

  return (
    <div className="flex flex-col gap-3 text-text">
      <h2 id={headingId} className="font-semibold text-base text-text">
        Share {label}
      </h2>

      {permissions.length === 0 || permission === undefined ? (
        <p className="text-sm text-text-secondary">Sharing isn't enabled for {label} records.</p>
      ) : (
        <form
          className="flex flex-col gap-3"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <FieldWrapper label="Email" error={emailError}>
            <TextInput
              ref={emailRef}
              type="email"
              autoComplete="off"
              value={email}
              disabled={isGranting}
              onChange={setEmail}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                void submit();
              }}
            />
          </FieldWrapper>
          <div className="grid grid-cols-[auto_1fr] gap-2">
            {permissions.length > 1 && (
              <div role="radiogroup" aria-labelledby={accessId} className="flex flex-col gap-1">
                <span id={accessId} className="font-medium text-sm text-text">
                  Access
                </span>
                <SegmentedField
                  options={permissions.map((p) => ({ value: p, label: PERMISSION_LABELS[p] }))}
                  value={permission}
                  disabled={isGranting}
                  onChange={(value) => setPermission(value as SharePermission)}
                />
              </div>
            )}
            <DateField
              label="Expires (optional)"
              value={expires}
              min={todayAsDateFieldValue()}
              error={expiresError}
              disabled={isGranting}
              onChange={(date) => {
                setExpires(date);
                setExpiresError(undefined);
              }}
            />
          </div>
          {existingShare && (
            <p className="text-text-secondary text-xs">
              Already shared: {PERMISSION_LABELS[existingShare.permission]}
              {existingShare.expiresAt ? `, expires ${EXPIRY_FORMAT.format(new Date(existingShare.expiresAt))}` : ""}.
              Sharing again replaces their access and expiry.
            </p>
          )}
          {permissions.length === 1 && (
            <p className="text-xs text-text-secondary">
              They'll be able to {PERMISSION_VERBS[permission]} this {label}.
            </p>
          )}
          {formError && (
            <p role="alert" className="text-danger text-sm">
              {formError}
            </p>
          )}
          <div className="flex justify-end">
            <ActionButton variant="primary" loading={isGranting} onClick={() => void submit()}>
              Share
            </ActionButton>
          </div>
        </form>
      )}

      <div className="border-border border-t pt-3">
        <h3 className="mb-1 font-medium text-sm text-text">Shared with</h3>
        {isLoading ? (
          <Skeleton lines={2} />
        ) : isError ? (
          <div role="alert" className="flex flex-col items-start gap-2 text-sm">
            <div className="flex items-center gap-2">
              <Icon name="circle-alert" size={14} className="text-danger" aria-hidden="true" />
              <span className="text-text">Couldn't load who this is shared with.</span>
            </div>
            {error && <p className="text-text-secondary">{error.message}</p>}
            <ActionButton variant="secondary" size="sm" onClick={refetch}>
              Retry
            </ActionButton>
          </div>
        ) : shares.length === 0 ? (
          <p className="text-sm text-text-secondary">Not shared with anyone yet.</p>
        ) : (
          <ul className="max-h-60 overflow-y-auto">
            {shares.map((share) => (
              <ShareRow
                key={share.id}
                share={share}
                isRevoking={revokingIds.includes(share.id)}
                error={revokeErrors[share.id]}
                onRevoke={() => void handleRevoke(share.id)}
              />
            ))}
          </ul>
        )}
      </div>

      <span role="status" className="sr-only">
        {announcement}
      </span>
    </div>
  );
}
