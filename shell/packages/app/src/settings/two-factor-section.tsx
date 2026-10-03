import {
  beginTOTPEnrollment,
  confirmTOTPEnrollment,
  fetchMFAFactors,
  type MFACodeConfirmation,
  type MFAFactor,
  type MFAFactors,
  regenerateRecoveryCodes,
  removeMFAFactor,
  reverifyMFA,
  supportsPasskeys,
  useAuth,
} from "@goerp/sdk/auth";
import {
  Badge,
  Button,
  Checkbox,
  DataTable,
  type DataTableColumn,
  formatFieldValue,
  formatRelativeTime,
  SectionCard,
} from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, useEffect, useState } from "react";
import { defaultPasskeyEnrollmentClient, type PasskeyEnrollmentClient } from "../auth/passkey-enrollment-form.js";
import { RecoveryCodesList } from "../auth/totp-enrollment-parts.js";
import { type AddAuthenticatorClient, AddAuthenticatorSheet } from "./add-authenticator-sheet.js";
import { AddPasskeySheet } from "./add-passkey-sheet.js";
import { MFACodeForm, MFADialog } from "./mfa-code-dialog.js";
import { mfaFactorsQueryKey, sessionsQueryKey } from "./query-keys.js";

export interface TwoFactorClient extends AddAuthenticatorClient {
  list: (signal?: AbortSignal) => Promise<MFAFactors>;
  remove: (id: string, confirmation: MFACodeConfirmation) => Promise<void>;
  regenerate: (confirmation: MFACodeConfirmation) => Promise<string[]>;
}

export const defaultTwoFactorClient: TwoFactorClient = {
  list: fetchMFAFactors,
  remove: removeMFAFactor,
  regenerate: regenerateRecoveryCodes,
  begin: beginTOTPEnrollment,
  confirm: confirmTOTPEnrollment,
  reverify: reverifyMFA,
};

export const POLICY_REQUIRES_2FA = "Your organisation requires two-factor authentication";

const FACTOR_TYPE_LABELS: Record<MFAFactor["type"], string> = {
  totp: "Authenticator app",
  webauthn: "Passkey",
};

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

function enabledSummary(factors: MFAFactor[]): string {
  const apps = factors.filter((f) => f.type === "totp").length;
  const passkeys = factors.length - apps;
  return [
    apps > 0 ? plural(apps, "authenticator app", "authenticator apps") : null,
    passkeys > 0 ? plural(passkeys, "passkey", "passkeys") : null,
  ]
    .filter(Boolean)
    .join(" and ");
}

function factorName(factor: MFAFactor): string {
  return factor.label ?? FACTOR_TYPE_LABELS[factor.type].toLowerCase();
}

export function TwoFactorSection({
  client = defaultTwoFactorClient,
  passkeyClient = defaultPasskeyEnrollmentClient,
}: {
  client?: TwoFactorClient;
  passkeyClient?: PasskeyEnrollmentClient;
}): ReactNode {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: mfaFactorsQueryKey, queryFn: ({ signal }) => client.list(signal) });
  const [sheetOpen, setSheetOpen] = useState(false);
  const [passkeyOpen, setPasskeyOpen] = useState(false);
  const [passkeyRun, setPasskeyRun] = useState(0);
  const [removeOpen, setRemoveOpen] = useState(false);
  // Kept after the dialog closes, so its title holds through the exit animation.
  const [removeTarget, setRemoveTarget] = useState<MFAFactor | null>(null);
  const [regenerateOpen, setRegenerateOpen] = useState(false);

  const refresh = () => queryClient.invalidateQueries({ queryKey: mfaFactorsQueryKey });

  if (query.isError) {
    return (
      <SectionCard title="Two-factor authentication">
        <div role="alert" className="mt-3 flex flex-col items-start gap-2 text-sm">
          <span className="text-text">Couldn't load your two-factor settings.</span>
          <Button variant="secondary" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        </div>
      </SectionCard>
    );
  }

  const data = query.data;
  const factors = data?.factors ?? [];
  const enabled = factors.length > 0;
  const lastRequired = data?.requiredByPolicy === true && factors.length === 1;

  const columns: DataTableColumn<MFAFactor>[] = [
    { key: "type", header: "Method", render: (f) => FACTOR_TYPE_LABELS[f.type] },
    { key: "label", header: "Name", render: (f) => f.label ?? "—" },
    { key: "added", header: "Added", render: (f) => formatFieldValue(f.createdAt, "date", undefined, "—") },
    { key: "last-used", header: "Last used", render: (f) => formatRelativeTime(f.lastUsedAt, "Never") },
    {
      key: "actions",
      header: "",
      render: (f) =>
        lastRequired ? (
          <span className="text-sm text-text-secondary">{POLICY_REQUIRES_2FA}</span>
        ) : (
          <Button
            variant="secondary"
            size="sm"
            aria-label={`Remove ${factorName(f)}`}
            onClick={() => {
              setRemoveTarget(f);
              setRemoveOpen(true);
            }}
          >
            Remove
          </Button>
        ),
    },
  ];

  return (
    <SectionCard title="Two-factor authentication">
      <div className="mt-3 flex flex-col gap-4">
        {!query.isLoading && (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge label={enabled ? "On" : "Off"} color={enabled ? "green" : "gray"} />
            <span className="text-text-secondary">
              {enabled
                ? `Signing in uses ${enabledSummary(factors)}.`
                : "Add a two-factor method to protect your account when you sign in."}
            </span>
          </div>
        )}

        {(query.isLoading || enabled) && (
          <DataTable columns={columns} data={factors} keyExtractor={(f) => f.id} isLoading={query.isLoading} />
        )}

        {!query.isLoading && (
          <div className="flex flex-wrap gap-2">
            <Button variant={enabled ? "secondary" : "primary"} onClick={() => setSheetOpen(true)}>
              Add authenticator app
            </Button>
            {supportsPasskeys() && (
              <Button
                variant={enabled ? "secondary" : "primary"}
                onClick={() => {
                  setPasskeyRun((run) => run + 1);
                  setPasskeyOpen(true);
                }}
              >
                Add passkey
              </Button>
            )}
          </div>
        )}

        {enabled && data && (
          <div className="flex flex-col items-start gap-2 border-border border-t pt-4">
            <h3 className="font-medium text-sm text-text">Recovery codes</h3>
            <p className="text-sm text-text-secondary">
              {plural(data.recoveryCodesRemaining, "recovery code", "recovery codes")} left. Each one signs you in once
              if you can't use your two-factor method.
            </p>
            <Button variant="secondary" size="sm" onClick={() => setRegenerateOpen(true)}>
              Generate new codes
            </Button>
          </div>
        )}
      </div>

      <AddAuthenticatorSheet
        open={sheetOpen}
        client={client}
        canReverifyPasskey={factors.some((factor) => factor.type === "webauthn")}
        onEnrolled={() => {
          toast.success("Authenticator app added.");
          void refresh();
        }}
        onClose={() => setSheetOpen(false)}
      />

      <AddPasskeySheet
        key={passkeyRun}
        open={passkeyOpen}
        client={passkeyClient}
        canReverifyPasskey={factors.some((factor) => factor.type === "webauthn")}
        onEnrolled={() => {
          void refresh();
        }}
        onClose={(enrolled) => {
          setPasskeyOpen(false);
          if (enrolled) toast.success("Passkey added.");
        }}
      />

      <RemoveFactorDialog
        open={removeOpen}
        factor={removeTarget}
        isLast={factors.length === 1}
        client={client}
        onNotFound={() => {
          toast.info("That method has already been removed.");
          setRemoveOpen(false);
          void refresh();
        }}
        onCancel={() => setRemoveOpen(false)}
      />

      <RegenerateCodesDialog
        open={regenerateOpen}
        client={client}
        onRegenerated={(codes) => {
          queryClient.setQueryData<MFAFactors>(mfaFactorsQueryKey, (current) =>
            current ? { ...current, recoveryCodesRemaining: codes.length } : current,
          );
          // Regenerating signs out the user's other sessions.
          void queryClient.invalidateQueries({ queryKey: sessionsQueryKey });
        }}
        onClose={() => setRegenerateOpen(false)}
      />
    </SectionCard>
  );
}

interface RemoveFactorDialogProps {
  open: boolean;
  factor: MFAFactor | null;
  isLast: boolean;
  client: TwoFactorClient;
  onNotFound: () => void;
  onCancel: () => void;
}

function RemoveFactorDialog({
  open,
  factor,
  isLast,
  client,
  onNotFound,
  onCancel,
}: RemoveFactorDialogProps): ReactNode {
  const { expireSession } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const description = isLast
    ? "This turns off two-factor authentication and your recovery codes stop working. You'll be signed out everywhere, including this browser."
    : "You'll be signed out everywhere, including this browser. Enter a current code to confirm.";

  return (
    <MFADialog
      open={open}
      title={`Remove ${factor ? factorName(factor) : "this method"}?`}
      description={description}
      tone="warning"
      onDismiss={onCancel}
    >
      <MFACodeForm
        submitLabel="Remove"
        submitVariant="danger"
        onCancel={onCancel}
        describeError={(err) => {
          if (!isAppError(err)) return undefined;
          if (err.code === "mfa_required_by_policy") {
            return `${POLICY_REQUIRES_2FA}, so you can't remove your last method.`;
          }
          if (err.code === "mfa_factor_not_found") onNotFound();
          return undefined;
        }}
        onSubmit={async (confirmation) => {
          if (!factor) return;
          await client.remove(factor.id, confirmation);
          // Factor removal revokes this session, so its cached account data is no longer usable.
          queryClient.removeQueries({ queryKey: mfaFactorsQueryKey });
          queryClient.removeQueries({ queryKey: sessionsQueryKey });
          // Starting navigation before expiry prevents the sign-in modal from obscuring the login route.
          const landed = navigate({
            to: "/auth/login",
            search: { redirect: "/settings/security", notice: "mfa_factor_removed" },
            replace: true,
          });
          expireSession();
          await landed;
        }}
      />
    </MFADialog>
  );
}

interface RegenerateCodesDialogProps {
  open: boolean;
  client: TwoFactorClient;
  onRegenerated: (codes: string[]) => void;
  onClose: () => void;
}

function RegenerateCodesDialog({ open, client, onRegenerated, onClose }: RegenerateCodesDialogProps): ReactNode {
  const [codes, setCodes] = useState<string[] | null>(null);
  const [saved, setSaved] = useState(false);

  // Reset on open, not on close, so the codes stay up through the exit animation.
  useEffect(() => {
    if (!open) return;
    setCodes(null);
    setSaved(false);
  }, [open]);

  const close = onClose;

  return (
    <MFADialog
      open={open}
      title={codes ? "Your new recovery codes" : "Generate new recovery codes?"}
      description={
        codes
          ? "Save these codes somewhere safe. Each one signs you in once, and they won't be shown again."
          : "Your current recovery codes will stop working, and your other sessions will be signed out."
      }
      onDismiss={close}
      dismissible={codes === null}
    >
      {codes ? (
        <div className="flex flex-col gap-4">
          <RecoveryCodesList codes={codes} />
          <Checkbox label="I've saved these codes" checked={saved} onChange={setSaved} />
          <div className="flex justify-end">
            <Button variant="primary" disabled={!saved} onClick={close}>
              Done
            </Button>
          </div>
        </div>
      ) : (
        <MFACodeForm
          submitLabel="Generate new codes"
          onCancel={close}
          describeError={(err) =>
            isAppError(err) && err.code === "mfa_not_enrolled"
              ? "Add an authenticator app before generating recovery codes."
              : undefined
          }
          onSubmit={async (confirmation) => {
            const next = await client.regenerate(confirmation);
            setCodes(next);
            onRegenerated(next);
          }}
        />
      )}
    </MFADialog>
  );
}
