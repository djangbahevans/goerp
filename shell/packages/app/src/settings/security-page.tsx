import { type ActiveSession, fetchSessions, revokeOtherSessions, revokeSession } from "@goerp/sdk/auth";
import {
  ActionButton,
  AlertDialog,
  Badge,
  CountryFlag,
  DataTable,
  type DataTableColumn,
  formatFieldValue,
  formatRelativeTime,
  PageHeader,
  PageLayout,
  SectionCard,
} from "@goerp/sdk/components";
import { isAppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { describeUserAgent } from "../auth/describe-user-agent.js";
import { RouterTextLink } from "../router/text-link.js";
import { CHANGE_PASSWORD_ANCHOR } from "./change-password-section.js";

// Injectable for stories; the route uses the real auth client.
export interface SessionsClient {
  list: (signal?: AbortSignal) => Promise<ActiveSession[]>;
  revoke: (id: string) => Promise<void>;
  revokeOthers: () => Promise<number>;
}

const defaultClient: SessionsClient = {
  list: fetchSessions,
  revoke: revokeSession,
  revokeOthers: revokeOtherSessions,
};

export const sessionsQueryKey = ["auth", "sessions"] as const;

export interface SecurityPageProps {
  client?: SessionsClient | undefined;
}

// shell-ux.md §4.3. The two-factor section is goerp#1074; API keys stay out
// while GOERP_ENABLE_API_KEYS is off.
export function SecurityPage({ client = defaultClient }: SecurityPageProps): ReactNode {
  return (
    <PageLayout>
      <PageHeader title="Security" subtitle="See where you're signed in and manage your password." />
      <div className="flex flex-col gap-6">
        <ActiveSessionsSection client={client} />
        <SectionCard title="Password">
          <RouterTextLink to="/settings/profile" hash={CHANGE_PASSWORD_ANCHOR}>
            Change password
          </RouterTextLink>
        </SectionCard>
      </div>
    </PageLayout>
  );
}

function otherSessionsSignedOut(count: number): string {
  return `Signed out of ${count} other ${count === 1 ? "session" : "sessions"}`;
}

function ActiveSessionsSection({ client }: { client: SessionsClient }): ReactNode {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: sessionsQueryKey, queryFn: ({ signal }) => client.list(signal) });
  const [dialog, setDialog] = useState<"one" | "others" | null>(null);
  // Kept after the dialog closes, so its title holds through the exit animation.
  const [target, setTarget] = useState<ActiveSession | null>(null);

  const dropSession = (id: string) =>
    queryClient.setQueryData<ActiveSession[]>(sessionsQueryKey, (sessions) => sessions?.filter((s) => s.id !== id));

  const revoke = useMutation({
    mutationFn: (id: string) => client.revoke(id),
    onSuccess: (_, id) => dropSession(id),
    onError: (err, id) => {
      // Already ended elsewhere: the row is stale either way.
      if (isAppError(err) && err.code === "session_not_found") {
        dropSession(id);
        return;
      }
      toast.error("Couldn't sign out that session. Try again.");
    },
  });

  const revokeOthers = useMutation({
    mutationFn: () => client.revokeOthers(),
    onSuccess: (count) => toast.success(otherSessionsSignedOut(count)),
    onError: () => toast.error("Couldn't sign out your other sessions. Try again."),
    onSettled: () => queryClient.invalidateQueries({ queryKey: sessionsQueryKey }),
  });

  const sessions = query.data ?? [];
  const hasOthers = sessions.some((s) => !s.current);

  const columns: DataTableColumn<ActiveSession>[] = [
    {
      key: "device",
      header: "Device",
      render: (s) => (
        <span className="inline-flex flex-wrap items-center gap-2">
          {describeUserAgent(s.userAgent)}
          {s.current && <Badge label="This device" color="green" />}
        </span>
      ),
    },
    { key: "ip", header: "IP address", render: (s) => s.ipAddress ?? "—" },
    {
      key: "country",
      header: "Country",
      render: (s) => (s.countryCode ? <CountryFlag code={s.countryCode} showName /> : "—"),
    },
    {
      key: "signed-in",
      header: "Signed in",
      render: (s) => formatFieldValue(s.signedInAt, "datetime", undefined, "—"),
    },
    { key: "last-active", header: "Last active", render: (s) => formatRelativeTime(s.lastActiveAt, "—") },
    {
      key: "actions",
      header: "",
      render: (s) =>
        s.current ? null : (
          <ActionButton
            variant="secondary"
            size="sm"
            loading={revoke.isPending && revoke.variables === s.id}
            disabled={revoke.isPending || revokeOthers.isPending}
            onClick={() => {
              setTarget(s);
              setDialog("one");
            }}
          >
            Sign out
          </ActionButton>
        ),
    },
  ];

  return (
    <SectionCard title="Active sessions">
      {query.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2 text-sm">
          <span className="text-text">Couldn't load your sessions.</span>
          <ActionButton variant="secondary" size="sm" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : (
        <div className="mt-3 flex flex-col gap-3">
          {!query.isLoading &&
            (hasOthers ? (
              <div>
                <ActionButton
                  variant="secondary"
                  loading={revokeOthers.isPending}
                  disabled={revoke.isPending}
                  onClick={() => setDialog("others")}
                >
                  Sign out all other sessions
                </ActionButton>
              </div>
            ) : (
              <p className="text-sm text-text-secondary">You're not signed in anywhere else.</p>
            ))}
          <DataTable columns={columns} data={sessions} keyExtractor={(s) => s.id} isLoading={query.isLoading} />
        </div>
      )}
      <AlertDialog
        open={dialog === "one"}
        title={`Sign out ${describeUserAgent(target?.userAgent ?? null)}?`}
        description="That device will need to sign in again."
        tone="warning"
        confirmLabel="Sign out"
        confirmVariant="danger"
        onCancel={() => setDialog(null)}
        onConfirm={() => {
          if (target) revoke.mutate(target.id);
          setDialog(null);
        }}
      />
      <AlertDialog
        open={dialog === "others"}
        title="Sign out all other sessions?"
        description="Every device except this one will need to sign in again."
        tone="warning"
        confirmLabel="Sign out all"
        confirmVariant="danger"
        onCancel={() => setDialog(null)}
        onConfirm={() => {
          revokeOthers.mutate();
          setDialog(null);
        }}
      />
    </SectionCard>
  );
}
