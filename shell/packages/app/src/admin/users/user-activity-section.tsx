import {
  ActionButton,
  EmptyState,
  Icon,
  SectionCard,
  SegmentedField,
  Skeleton,
  Timeline,
  TimelineItem,
} from "@goerp/sdk/components";
import { modelRegistry } from "@goerp/sdk/schema";
import { useQueries } from "@tanstack/react-query";
import { type ReactNode, useId, useState } from "react";
import { type AdminActivityEntry, type AdminActivityFilter, useAdminUserActivity } from "./admin-users-api.js";
import { roleLabel } from "./roles.js";

const FILTERS: { value: AdminActivityFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "auth", label: "Account" },
  { value: "data", label: "Records" },
];

const EMPTY_DESCRIPTIONS: Record<AdminActivityFilter, string> = {
  all: "Sign-ins, account changes and record changes in this organisation show up here.",
  auth: "Sign-ins and account changes in this organisation show up here.",
  data: "Changes this user makes to records show up here.",
};

// auth-internals.md §17's event types. An event type missing here shows
// as its raw name rather than being hidden.
const AUTH_TITLES: Record<string, string> = {
  "login.success": "Signed in",
  "login.failure": "Sign-in failed",
  "login.mfa_required": "Sign-in waiting for two-factor verification",
  "login.oauth": "Signed in with a connected account",
  "login.saml": "Signed in with single sign-on",
  "mfa.verified": "Two-factor verification passed",
  "mfa.failed": "Two-factor code rejected",
  "mfa.enrolled": "Two-factor method added",
  "mfa.revoked": "Two-factor method removed",
  "mfa.recovery_codes_regenerated": "Recovery codes regenerated",
  "mfa.clone_suspected": "Passkey revoked after suspected cloning",
  "mfa.admin_reset": "Two-factor reset",
  "session.refresh": "Session refreshed",
  "session.refresh_replay": "Session ended after a reused token",
  "session.logout": "Signed out",
  "session.revoked": "Session revoked",
  "password.reset_requested": "Password reset requested",
  "password.reset_completed": "Password reset",
  "password.changed": "Password changed",
  "user.provider_access_revoked": "Connected account access revoked",
  "user.invited": "Invited",
  "user.invite_accepted": "Invitation accepted",
  "user.invite_expired": "Invitation expired",
  "user.invite_resent": "Invitation resent",
  "user.invite_revoked": "Invitation revoked",
  "user.suspended": "Account suspended",
  "user.unsuspended": "Account unsuspended",
  "user.deleted": "Account deleted",
  "account.locked": "Account locked",
  "account.unlocked": "Account unlocked",
  "api_key.created": "API key created",
  "api_key.used": "API key used",
  "api_key.revoked": "API key revoked",
  "role.granted": "Role added",
  "role.revoked": "Role removed",
  "role.created": "Role created",
  "role.updated": "Role updated",
  "role.deleted": "Role deleted",
  "tenant.settings_updated": "Organisation settings changed",
  "key.compromised": "Key marked compromised",
  "permission.denied": "Permission denied",
  "field.read_denied": "Field read blocked",
  "field.write_denied": "Field change blocked",
};

const RECORD_VERBS: Record<string, string> = {
  "record.created": "created",
  "record.updated": "updated",
  "record.deleted": "deleted",
};

const RECORD_ICONS: Record<string, string> = {
  "record.created": "file-plus",
  "record.updated": "file-pen",
  "record.deleted": "file-x",
};

function metadataString(entry: AdminActivityEntry, key: string): string | undefined {
  const value = entry.metadata?.[key];
  return typeof value === "string" && value !== "" ? value : undefined;
}

function authTitle(entry: AdminActivityEntry): string {
  const role = metadataString(entry, entry.action === "role.created" ? "name" : "role");
  if (role && (entry.action === "role.granted" || entry.action === "role.revoked" || entry.action === "role.created")) {
    return `${AUTH_TITLES[entry.action]}: ${roleLabel(role)}`;
  }
  return AUTH_TITLES[entry.action] ?? entry.action;
}

function humanize(code: string): string {
  const words = code.replaceAll("_", " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function personName(person: { name: string | null }): string {
  return person.name ?? "Unknown user";
}

// The model's label from the schema, falling back to the model's own name
// when the schema doesn't have it.
function recordLabel(model: string | null, labels: Map<string, string>): string {
  if (!model) return "Record";
  return labels.get(model) ?? model.slice(model.indexOf(".") + 1);
}

function useModelLabels(entries: AdminActivityEntry[]): Map<string, string> {
  const models = [...new Set(entries.flatMap((entry) => (entry.record?.model ? [entry.record.model] : [])))];
  const results = useQueries({
    queries: models.map((model) => ({
      queryKey: ["model-label", model],
      queryFn: async () => (await modelRegistry.resolve(model)).label,
      retry: false,
      staleTime: Number.POSITIVE_INFINITY,
    })),
  });
  const labels = new Map<string, string>();
  models.forEach((model, i) => {
    const label = results[i]?.data;
    if (label) labels.set(model, label);
  });
  return labels;
}

interface EntryDisplay {
  icon: string;
  danger: boolean;
  title: string;
  description?: string | undefined;
  detail?: ReactNode;
}

function entryDisplay(entry: AdminActivityEntry, userId: string, labels: Map<string, string>): EntryDisplay {
  if (entry.source === "data") {
    const fields = entry.changedFields ?? [];
    return {
      icon: RECORD_ICONS[entry.action] ?? "file",
      danger: false,
      title: `${recordLabel(entry.record?.model ?? null, labels)} ${RECORD_VERBS[entry.action] ?? entry.action}`,
      description: fields.length > 0 ? `Changed ${fields.join(", ")}` : undefined,
      detail: entry.record && <span className="font-mono text-text-secondary text-xs">{entry.record.id}</span>,
    };
  }
  // Another user's account, when this user acted on it (an admin suspending
  // someone, say).
  const subject = entry.user && entry.user.id !== userId ? `${personName(entry.user)}'s account` : undefined;
  const reason = !entry.success && entry.failureReason ? `Reason: ${humanize(entry.failureReason)}` : undefined;
  return {
    icon: entry.success ? "shield" : "circle-alert",
    danger: !entry.success,
    title: authTitle(entry),
    description: [subject, reason].filter(Boolean).join(" · ") || undefined,
  };
}

// shell-ux.md §5.1 "User detail page": the user's recent activity in this
// tenant, from GET /admin/users/{id}/activity (auth-internals.md §17).
export function UserActivitySection({ userId }: { userId: string }): ReactNode {
  const [filter, setFilter] = useState<AdminActivityFilter>("all");
  const query = useAdminUserActivity(userId, filter);
  const entries = query.data?.pages.flatMap((page) => page.entries) ?? [];
  const labels = useModelLabels(entries);
  const filterLabelId = useId();

  function renderFeed(): ReactNode {
    if (query.isLoading) return <Skeleton lines={3} />;
    if (entries.length === 0 && query.isError) {
      return (
        <div role="alert" className="flex flex-col items-center gap-2 py-4 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-sm text-text">Couldn't load activity.</p>
          <ActionButton variant="secondary" size="sm" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      );
    }
    if (entries.length === 0) {
      return <EmptyState size="compact" title="No activity yet" description={EMPTY_DESCRIPTIONS[filter]} />;
    }
    return (
      <>
        <Timeline>
          {entries.map((entry) => {
            const display = entryDisplay(entry, userId, labels);
            const byOther = entry.actor && entry.actor.id !== userId ? entry.actor : null;
            return (
              <TimelineItem
                key={`${entry.source}-${entry.id}`}
                icon={
                  <Icon
                    name={display.icon}
                    size={16}
                    className={display.danger ? "text-danger" : "text-text-secondary"}
                    aria-hidden="true"
                  />
                }
                title={display.title}
                timestamp={entry.occurredAt}
                description={display.description}
                user={byOther ? { name: personName(byOther) } : undefined}
              >
                {display.detail}
              </TimelineItem>
            );
          })}
        </Timeline>
        {query.isError && (
          <p role="alert" className="mt-4 text-center text-danger text-sm">
            {query.isFetchNextPageError ? "Couldn't load more activity." : "Couldn't refresh activity."}
          </p>
        )}
        {query.hasNextPage && (
          <div className="mt-4 flex justify-center">
            <ActionButton
              variant="secondary"
              size="sm"
              loading={query.isFetchingNextPage}
              onClick={() => void query.fetchNextPage()}
            >
              Load more
            </ActionButton>
          </div>
        )}
      </>
    );
  }

  return (
    <SectionCard title="Activity">
      <div className="flex flex-col gap-4">
        <div role="radiogroup" aria-labelledby={filterLabelId} className="flex items-center gap-2">
          <span id={filterLabelId} className="sr-only">
            Show activity
          </span>
          <SegmentedField
            options={FILTERS}
            value={filter}
            onChange={(value) => setFilter(value as AdminActivityFilter)}
          />
        </div>
        <div>{renderFeed()}</div>
      </div>
    </SectionCard>
  );
}
