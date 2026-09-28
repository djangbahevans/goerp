import {
  Button,
  Checkbox,
  DataTable,
  type DataTableColumn,
  EmptyState,
  FieldWrapper,
  PageHeader,
  PageLayout,
  SectionCard,
  Skeleton,
  ToggleField,
} from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import {
  type NotificationTypeEntry,
  type NotificationTypeGroup,
  useViewRegistryStatus,
  ViewRegistryContext,
} from "@goerp/sdk/schema";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check } from "lucide-react";
import { type ReactNode, useContext, useRef } from "react";
import {
  applyPreferencesPatch,
  type NotificationPreferences,
  type NotificationPreferencesClient,
  type NotificationPreferencesPatch,
  notificationPreferencesClient,
  TOGGLE_CHANNELS,
  type ToggleChannel,
  typeSettings,
} from "./notification-preferences.js";
import { notificationPreferencesQueryKey } from "./query-keys.js";

const CHANNEL_LABELS: Record<ToggleChannel, string> = { email: "Email", sms: "SMS", push: "Push" };

export interface NotificationsPageProps {
  client?: NotificationPreferencesClient | undefined;
}

// shell-ux.md §4.2: every toggle saves on change, applied optimistically
// and reverted with a toast if the save fails.
export function NotificationsPage({ client = notificationPreferencesClient }: NotificationsPageProps): ReactNode {
  const registry = useContext(ViewRegistryContext);
  const registryStatus = useViewRegistryStatus();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: notificationPreferencesQueryKey,
    queryFn: ({ signal }) => client.get(signal),
  });
  // Per toggle, the latest save: an earlier save that fails after a later
  // change to the same toggle doesn't revert that change.
  const latestSave = useRef<Record<string, number>>({});

  const setCached = (patch: NotificationPreferencesPatch) =>
    queryClient.setQueryData<NotificationPreferences>(
      notificationPreferencesQueryKey,
      (prefs) => prefs && applyPreferencesPatch(prefs, patch),
    );
  const onlySave = () => queryClient.isMutating({ mutationKey: notificationPreferencesQueryKey }) === 1;

  const save = useMutation({
    mutationKey: notificationPreferencesQueryKey,
    mutationFn: ({
      patch,
    }: {
      key: string;
      token: number;
      patch: NotificationPreferencesPatch;
      revert: NotificationPreferencesPatch;
    }) => client.update(patch),
    onMutate: async ({ patch }) => {
      // Awaited: cancelling an in-flight fetch reverts the cache, which
      // would otherwise land on top of the optimistic write.
      await queryClient.cancelQueries({ queryKey: notificationPreferencesQueryKey });
      setCached(patch);
    },
    onSuccess: (prefs) => {
      // A response to an earlier save lacks the optimistic state of any
      // save still in flight.
      if (onlySave()) queryClient.setQueryData(notificationPreferencesQueryKey, prefs);
    },
    onError: (_err, { key, token, revert }) => {
      if (latestSave.current[key] !== token) return;
      setCached(revert);
      toast.error("Couldn't save your notification settings. Try again.");
      if (onlySave()) void queryClient.invalidateQueries({ queryKey: notificationPreferencesQueryKey });
    },
  });

  const change = (key: string, patch: NotificationPreferencesPatch, revert: NotificationPreferencesPatch) => {
    const token = (latestSave.current[key] ?? 0) + 1;
    latestSave.current[key] = token;
    save.mutate({ key, token, patch, revert });
  };

  const changeGlobal = (channel: ToggleChannel, value: boolean) =>
    change(`global.${channel}`, { global: { [channel]: value } }, { global: { [channel]: !value } });

  const changeType = (type: string, channel: ToggleChannel, value: boolean) =>
    change(
      `${type}.${channel}`,
      { types: { [type]: { [channel]: value } } },
      { types: { [type]: { [channel]: !value } } },
    );

  return (
    <PageLayout>
      <PageHeader title="Notifications" subtitle="Choose how you're notified. In-app notifications are always on." />
      {query.isError || registryStatus === "error" ? (
        <div role="alert" className="flex flex-col items-start gap-2 text-sm">
          <span className="text-text">Couldn't load your notification settings.</span>
          <Button variant="secondary" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        </div>
      ) : !query.data || registryStatus === "loading" ? (
        <Skeleton type="card" />
      ) : (
        <PreferencesForm
          prefs={query.data}
          groups={registry?.notificationTypes ?? []}
          onGlobalChange={changeGlobal}
          onTypeChange={changeType}
        />
      )}
    </PageLayout>
  );
}

interface PreferencesFormProps {
  prefs: NotificationPreferences;
  groups: NotificationTypeGroup[];
  onGlobalChange: (channel: ToggleChannel, value: boolean) => void;
  onTypeChange: (type: string, channel: ToggleChannel, value: boolean) => void;
}

function PreferencesForm({ prefs, groups, onGlobalChange, onTypeChange }: PreferencesFormProps): ReactNode {
  const channels = TOGGLE_CHANNELS.filter((c) => prefs.availableChannels.includes(c));

  const columns: DataTableColumn<NotificationTypeEntry>[] = [
    {
      key: "type",
      header: "Notification type",
      render: (t) => (
        <span className="flex flex-col">
          <span>{t.label}</span>
          {t.description && <span className="text-sm text-text-secondary">{t.description}</span>}
        </span>
      ),
    },
    {
      key: "in_app",
      header: "In-app",
      render: () => (
        <span className="inline-flex items-center gap-1 text-sm text-text-secondary">
          Always
          <Check size={14} aria-hidden="true" />
        </span>
      ),
    },
    ...channels.map(
      (channel): DataTableColumn<NotificationTypeEntry> => ({
        key: channel,
        header: CHANNEL_LABELS[channel],
        render: (t) =>
          t.availableChannels.includes(channel) ? (
            <Checkbox
              label={`${CHANNEL_LABELS[channel]} for ${t.label}`}
              labelHidden
              checked={typeSettings(prefs, t.type)[channel]}
              onChange={(value) => onTypeChange(t.type, channel, value)}
            />
          ) : (
            <span className="text-text-secondary">
              <span aria-hidden="true">—</span>
              <span className="sr-only">Not available</span>
            </span>
          ),
      }),
    ),
  ];

  return (
    <div className="flex flex-col gap-6">
      <SectionCard title="Channels">
        <div className="mt-3 flex flex-col gap-4">
          {channels.map((channel) => (
            <FieldWrapper
              key={channel}
              label={CHANNEL_LABELS[channel]}
              description={`Turns ${CHANNEL_LABELS[channel]} on or off for every notification type, unless you change it for a type below.`}
            >
              <ToggleField value={prefs.global[channel]} onChange={(value) => onGlobalChange(channel, value)} />
            </FieldWrapper>
          ))}
        </div>
      </SectionCard>
      {groups.length === 0 ? (
        <EmptyState
          icon="bell-off"
          title="No notification types"
          description="None of your installed modules send notifications you can configure."
        />
      ) : (
        groups.map((group) => (
          <SectionCard key={group.module} title={group.displayName}>
            <div className="mt-3">
              <DataTable columns={columns} data={group.types} keyExtractor={(t) => t.type} />
            </div>
          </SectionCard>
        ))
      )}
    </div>
  );
}
