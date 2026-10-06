import { useTenant, useUser } from "@goerp/sdk/auth";
import {
  ActionButton,
  AlertDialog,
  Button,
  EmptyState,
  FieldWrapper,
  formatRelativeTime,
  Icon,
  PageLayout,
  SectionCard,
  Select,
  Skeleton,
  UserAvatar,
} from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { type ReactNode, useState } from "react";
import { describeUserAgent } from "../../auth/describe-user-agent.js";
import {
  type AdminUserDetail,
  type AdminUserSession,
  useAdminUser,
  useAdminUserSessions,
  useAssignRole,
  useRemoveUser,
  useResendInvitation,
  useResetUserMfa,
  useRevokeRole,
  useRevokeSession,
  useSuspendUser,
  useUnsuspendUser,
} from "./admin-users-api.js";
import { displayName } from "./admin-users-page.js";
import { ResetMfaDialog } from "./reset-mfa-dialog.js";
import { roleLabel, useAssignableRoles } from "./roles.js";
import { UserActivitySection } from "./user-activity-section.js";
import { UserStatusBadge } from "./user-status-badge.js";

const OTHER_ORGANISATIONS_UNAFFECTED = "Other organisations they belong to aren't affected.";

function failureMessage(err: unknown, fallback: string): string {
  if (err instanceof AppError && err.code === "last_admin") {
    return "This is the organisation's last active admin. Make someone else an admin first.";
  }
  return err instanceof AppError && err.message ? err.message : fallback;
}

export interface AdminUserDetailPageProps {
  userId: string;
  onBackToList: () => void;
}

// shell-ux.md §5.1 "User detail page".
export function AdminUserDetailPage({ userId, onBackToList }: AdminUserDetailPageProps): ReactNode {
  const query = useAdminUser(userId);

  if (query.isLoading) {
    return (
      <PageLayout>
        <Skeleton type="card" lines={4} />
      </PageLayout>
    );
  }
  if (query.error instanceof AppError && query.error.httpStatus === 404) {
    return (
      <PageLayout>
        <EmptyState
          icon="user-x"
          title="User not found"
          description="They may have been deleted, or they aren't part of this organisation."
          action={
            <Button variant="secondary" onClick={onBackToList}>
              Back to users
            </Button>
          }
        />
      </PageLayout>
    );
  }
  if (query.isError || !query.data) {
    return (
      <PageLayout>
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load this user.</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      </PageLayout>
    );
  }

  return <UserDetail user={query.data} onRemoved={onBackToList} />;
}

type Dialog = "suspend" | "remove" | "reset-mfa" | null;

function UserDetail({ user, onRemoved }: { user: AdminUserDetail; onRemoved: () => void }): ReactNode {
  const me = useUser();
  const tenant = useTenant();
  const isSelf = me.id === user.id;
  const isMember = user.invitationId === null;
  const [dialog, setDialog] = useState<Dialog>(null);

  const suspend = useSuspendUser(user.id);
  const unsuspend = useUnsuspendUser(user.id);
  const remove = useRemoveUser(user.id);
  const resetMfa = useResetUserMfa(user.id);
  const resend = useResendInvitation();

  const run = async (action: () => Promise<unknown>, success: string, failure: string) => {
    try {
      await action();
      toast.success(success);
      return true;
    } catch (err) {
      toast.error(failureMessage(err, failure));
      return false;
    }
  };

  const name = displayName(user);
  const statusAction = (() => {
    if (user.status === "invited" && user.invitation) {
      const invitationId = user.invitation.id;
      return (
        <ActionButton
          variant="secondary"
          loading={resend.isPending}
          onClick={() =>
            void run(() => resend.mutateAsync(invitationId), "Invitation resent.", "The invitation couldn't be resent.")
          }
        >
          Resend invite
        </ActionButton>
      );
    }
    if (user.status === "suspended") {
      return (
        <ActionButton
          variant="secondary"
          loading={unsuspend.isPending}
          onClick={() =>
            void run(() => unsuspend.mutateAsync(), `${name} was unsuspended.`, "The user couldn't be unsuspended.")
          }
        >
          Unsuspend user
        </ActionButton>
      );
    }
    if (user.status === "active" && isMember && !isSelf) {
      return (
        <ActionButton variant="secondary" loading={suspend.isPending} onClick={() => setDialog("suspend")}>
          Suspend user
        </ActionButton>
      );
    }
    return null;
  })();

  return (
    <PageLayout>
      <div className="flex flex-col gap-6">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex items-center gap-4">
            <UserAvatar userId={user.id} name={name} avatarUrl={user.avatarUrl} size="lg" />
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-2">
                <h1 className="font-semibold text-text text-xl">{name}</h1>
                <UserStatusBadge status={user.status} accountSuspended={user.accountSuspended} />
              </div>
              <dl className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-text-secondary">
                <div className="flex gap-1">
                  <dt className="sr-only">Email</dt>
                  <dd>{user.email}</dd>
                </div>
                {user.jobTitle && (
                  <div className="flex gap-1">
                    <dt className="sr-only">Job title</dt>
                    <dd>{user.jobTitle}</dd>
                  </div>
                )}
                {user.phone && (
                  <div className="flex gap-1">
                    <dt className="sr-only">Phone</dt>
                    <dd>{user.phone}</dd>
                  </div>
                )}
                <div className="flex gap-1">
                  <dt>Last login</dt>
                  <dd>{formatRelativeTime(user.lastLoginAt, "never")}</dd>
                </div>
              </dl>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            {statusAction}
            {isMember && (
              <ActionButton variant="secondary" loading={resetMfa.isPending} onClick={() => setDialog("reset-mfa")}>
                Reset two-factor authentication
              </ActionButton>
            )}
            {isMember && !isSelf && (
              <ActionButton variant="danger" loading={remove.isPending} onClick={() => setDialog("remove")}>
                Remove from {tenant.name}
              </ActionButton>
            )}
          </div>
        </div>

        {isMember ? <RolesSection user={user} isSelf={isSelf} /> : <InvitationSection user={user} />}
        {isMember && <SessionsSection userId={user.id} />}
        <UserActivitySection userId={user.id} />
      </div>

      <AlertDialog
        open={dialog === "suspend"}
        title={`Suspend ${name}?`}
        description={`${name} won't be able to sign in to ${tenant.name}. ${OTHER_ORGANISATIONS_UNAFFECTED}`}
        tone="warning"
        confirmLabel="Suspend user"
        confirmVariant="danger"
        input={{ label: "Reason", type: "text", required: true, placeholder: "Why is this account being suspended?" }}
        onCancel={() => setDialog(null)}
        onConfirm={(reason) => {
          setDialog(null);
          void run(
            () => suspend.mutateAsync((reason ?? "").trim()),
            `${name} was suspended.`,
            "The user couldn't be suspended.",
          );
        }}
      />
      <AlertDialog
        open={dialog === "remove"}
        title={`Remove ${name} from ${tenant.name}?`}
        description={`This ends their access to ${tenant.name} and removes their roles. Their GoERP account is untouched, and they can be invited again later. ${OTHER_ORGANISATIONS_UNAFFECTED}`}
        tone="danger"
        confirmLabel={`Remove from ${tenant.name}`}
        confirmVariant="danger"
        requireTyping={user.email}
        onCancel={() => setDialog(null)}
        onConfirm={() => {
          setDialog(null);
          void run(
            () => remove.mutateAsync(),
            `${name} was removed from ${tenant.name}.`,
            "The user couldn't be removed.",
          ).then((removed) => {
            if (removed) onRemoved();
          });
        }}
      />
      <ResetMfaDialog
        open={dialog === "reset-mfa"}
        name={name}
        tenantName={tenant.name}
        onClose={() => setDialog(null)}
        onConfirm={async (password) => {
          await resetMfa.mutateAsync(password);
          setDialog(null);
          toast.success(`Two-factor authentication was reset for ${name}.`);
        }}
      />
    </PageLayout>
  );
}

function RolesSection({ user, isSelf }: { user: AdminUserDetail; isSelf: boolean }): ReactNode {
  const assign = useAssignRole(user.id);
  const revoke = useRevokeRole(user.id);
  const assignable = useAssignableRoles();
  const available = assignable.filter((option) => !user.roles.includes(option.value));
  const [picked, setPicked] = useState("");
  const toAdd = available.some((option) => option.value === picked) ? picked : "";

  const add = async () => {
    try {
      await assign.mutateAsync(toAdd);
      toast.success(`Added the ${roleLabel(toAdd)} role.`);
      setPicked("");
    } catch (err) {
      toast.error(failureMessage(err, "The role couldn't be added."));
    }
  };

  const removeRole = async (role: string) => {
    try {
      await revoke.mutateAsync(role);
      toast.success(`Removed the ${roleLabel(role)} role.`);
    } catch (err) {
      toast.error(failureMessage(err, "The role couldn't be removed."));
    }
  };

  // A member's last role is their membership, and an admin's own admin role
  // is their access to this page, so neither is removable here.
  const lockReason = (role: string) => {
    if (user.roles.length === 1) return "A member needs at least one role.";
    if (isSelf && role === "admin") return "You can't remove your own admin role.";
    return undefined;
  };

  return (
    <SectionCard title="Roles">
      <ul className="flex flex-col divide-y divide-border">
        {user.roles.map((role) => {
          const locked = lockReason(role);
          return (
            <li key={role} className="flex items-center justify-between gap-4 py-2">
              <span className="text-text">{roleLabel(role)}</span>
              <span className="flex items-center gap-2">
                {locked && <span className="text-sm text-text-secondary">{locked}</span>}
                <ActionButton
                  variant="secondary"
                  size="sm"
                  disabled={locked !== undefined || revoke.isPending}
                  onClick={() => void removeRole(role)}
                >
                  Remove
                </ActionButton>
              </span>
            </li>
          );
        })}
      </ul>
      {available.length > 0 && (
        <div className="mt-3 flex items-end gap-2">
          <div className="w-48">
            <FieldWrapper label="Add role">
              <Select
                options={available}
                value={toAdd}
                placeholder="Choose a role"
                onChange={(value) => setPicked(typeof value === "string" ? value : "")}
              />
            </FieldWrapper>
          </div>
          <ActionButton variant="secondary" disabled={!toAdd} loading={assign.isPending} onClick={() => void add()}>
            Add role
          </ActionButton>
        </div>
      )}
    </SectionCard>
  );
}

function InvitationSection({ user }: { user: AdminUserDetail }): ReactNode {
  if (!user.invitation) return null;
  const expired = new Date(user.invitation.expiresAt).getTime() < Date.now();
  return (
    <SectionCard title="Invitation">
      <p className="text-text">
        Invited as {roleLabel(user.invitation.role)} {formatRelativeTime(user.invitation.createdAt, "")}.{" "}
        {expired
          ? "The invitation has expired; resend it to send a new link."
          : `The link expires ${formatRelativeTime(user.invitation.expiresAt, "")}.`}
      </p>
    </SectionCard>
  );
}

function SessionsSection({ userId }: { userId: string }): ReactNode {
  const query = useAdminUserSessions(userId, true);
  const revoke = useRevokeSession(userId);
  const [revoking, setRevoking] = useState<string | null>(null);

  const revokeSession = async (session: AdminUserSession) => {
    setRevoking(session.id);
    try {
      await revoke.mutateAsync(session.id);
      toast.success(`Signed out ${describeUserAgent(session.userAgent)}.`);
    } catch (err) {
      toast.error(failureMessage(err, "The session couldn't be revoked."));
    } finally {
      setRevoking(null);
    }
  };

  return (
    <SectionCard title="Active sessions">
      {query.isLoading ? (
        <Skeleton lines={2} />
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2 text-sm">
          <span className="text-text">Couldn't load sessions.</span>
          <ActionButton variant="secondary" size="sm" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : (query.data ?? []).length === 0 ? (
        <p className="text-sm text-text-secondary">No active sessions.</p>
      ) : (
        <ul className="flex flex-col divide-y divide-border">
          {(query.data ?? []).map((session) => (
            <li key={session.id} className="flex items-center justify-between gap-4 py-2">
              <div className="flex flex-col">
                <span className="text-text">
                  {describeUserAgent(session.userAgent)}
                  {session.current && <span className="ms-2 text-sm text-text-secondary">(this session)</span>}
                </span>
                <span className="text-sm text-text-secondary">
                  {[session.ipAddress, session.countryCode].filter(Boolean).join(" · ")}
                  {session.ipAddress || session.countryCode ? " · " : ""}
                  Active {formatRelativeTime(session.lastActiveAt, "")} · Signed in{" "}
                  {formatRelativeTime(session.signedInAt, "")}
                </span>
              </div>
              {!session.current && (
                <ActionButton
                  variant="secondary"
                  size="sm"
                  loading={revoking === session.id}
                  disabled={revoking !== null && revoking !== session.id}
                  onClick={() => void revokeSession(session)}
                >
                  Revoke
                </ActionButton>
              )}
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}
