import {
  Button,
  Checkbox,
  DataTable,
  type DataTableColumn,
  FieldWrapper,
  TextInput,
  ToggleField,
} from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { ViewRegistryContext } from "@goerp/sdk/schema";
import { Check } from "lucide-react";
import { type ReactNode, useContext, useState } from "react";
import { errorFor, type FieldError, reportSaveError, SettingsSection } from "./settings-section.js";
import {
  type NotificationDelivery,
  type NotificationDeliveryPatch,
  type NotificationTypeInfo,
  useUpdateNotificationDelivery,
} from "./tenant-settings-api.js";

type Channel = "email" | "sms" | "push";

const CHANNELS: { channel: Channel; label: string; switchKey: keyof NotificationDelivery["channels"] }[] = [
  { channel: "email", label: "Email", switchKey: "emailEnabled" },
  { channel: "sms", label: "SMS", switchKey: "smsEnabled" },
  { channel: "push", label: "Push", switchKey: "pushEnabled" },
];

const SWITCH_FIELDS: Record<keyof NotificationDelivery["channels"], string> = {
  emailEnabled: "channels.email_enabled",
  smsEnabled: "channels.sms_enabled",
  pushEnabled: "channels.push_enabled",
};

const FIELDS = ["channels", "sms", "defaults"] as const;
const LOCKED_NOTE = "Set by your platform operator.";
const ENGINE_MODULE = "engine";

interface DeliveryDraft {
  channels: NotificationDelivery["channels"];
  senderId: string;
  // Only the types with a tenant default; the rest follow their manifest.
  defaults: Record<string, string[]>;
}

function draftOf(d: NotificationDelivery): DeliveryDraft {
  return { channels: d.channels, senderId: d.sms.senderId, defaults: d.defaults };
}

function sameChannels(a: string[] | undefined, b: string[] | undefined): boolean {
  return a !== undefined && b !== undefined && a.length === b.length && a.every((c) => b.includes(c));
}

function toPatch(draft: DeliveryDraft, saved: DeliveryDraft): NotificationDeliveryPatch {
  const patch: NotificationDeliveryPatch = {};
  const channels: NonNullable<NotificationDeliveryPatch["channels"]> = {};
  for (const key of Object.keys(draft.channels) as (keyof DeliveryDraft["channels"])[]) {
    if (draft.channels[key] !== saved.channels[key]) channels[key] = draft.channels[key];
  }
  if (Object.keys(channels).length > 0) patch.channels = channels;
  if (draft.senderId !== saved.senderId) patch.sms = { senderId: draft.senderId };

  const defaults: Record<string, string[] | null> = {};
  for (const type of new Set([...Object.keys(saved.defaults), ...Object.keys(draft.defaults)])) {
    const next = draft.defaults[type];
    if (next === undefined) defaults[type] = null;
    else if (!sameChannels(next, saved.defaults[type])) defaults[type] = next;
  }
  if (Object.keys(defaults).length > 0) patch.defaults = defaults;
  return patch;
}

interface TypeRow extends NotificationTypeInfo {
  moduleName: string;
}

// Engine types lead as "General", then modules by display name, the way
// the members' own notification settings list them. One table keeps the
// channel columns aligned across modules.
function typeRows(types: NotificationTypeInfo[], displayNameOf: (module: string) => string): TypeRow[] {
  const rank = (t: NotificationTypeInfo) => (t.module === ENGINE_MODULE ? 0 : 1);
  return types
    .map((t) => ({ ...t, moduleName: t.module === ENGINE_MODULE ? "General" : displayNameOf(t.module) }))
    .sort((a, b) => rank(a) - rank(b) || a.moduleName.localeCompare(b.moduleName) || a.label.localeCompare(b.label));
}

export interface NotificationDeliverySectionProps {
  saved: NotificationDelivery;
}

// shell-ux.md §5.5 "Notification delivery": the channel switches are a hard
// gate over every notification, while the per-type defaults are only what a
// member gets until they choose for themselves (notification-system.md §8).
export function NotificationDeliverySection({ saved }: NotificationDeliverySectionProps): ReactNode {
  const initial = draftOf(saved);
  const [draft, setDraft] = useState<DeliveryDraft>(initial);
  const [error, setError] = useState<FieldError | null>(null);
  const update = useUpdateNotificationDelivery();
  const registry = useContext(ViewRegistryContext);

  const isLocked = (field: string) => saved.locked.includes(field);
  const defaultsLocked = isLocked("defaults");
  const patch = toPatch(draft, initial);
  const dirty = Object.keys(patch).length > 0;

  const change = (next: (d: DeliveryDraft) => DeliveryDraft) => {
    setDraft(next);
    setError(null);
  };

  const effective = (t: NotificationTypeInfo) => draft.defaults[t.type] ?? t.defaultChannels;

  const setTypeChannel = (t: NotificationTypeInfo, channel: Channel, on: boolean) =>
    change((d) => {
      const current = d.defaults[t.type] ?? t.defaultChannels;
      // In-app is always delivered, so it stays in the list whatever is picked.
      const next = t.availableChannels.filter((c) => c === "in_app" || (c === channel ? on : current.includes(c)));
      const { [t.type]: _, ...rest } = d.defaults;
      // Matching the manifest is the same as no tenant default, and storing
      // a copy would stop the type following a later manifest change.
      const manifest = t.availableChannels.filter((c) => c === "in_app" || t.defaultChannels.includes(c));
      return { ...d, defaults: sameChannels(next, manifest) ? rest : { ...rest, [t.type]: next } };
    });

  const resetType = (t: NotificationTypeInfo) =>
    change((d) => {
      const { [t.type]: _, ...rest } = d.defaults;
      return { ...d, defaults: rest };
    });

  const save = () =>
    update.mutate(patch, {
      onSuccess: (delivery) => {
        setDraft(draftOf(delivery));
        toast.success("Notification delivery settings saved.");
      },
      onError: (err) => setError(reportSaveError(err, FIELDS)),
    });

  const displayNameOf = (module: string) =>
    registry?.notificationTypes.find((g) => g.module === module)?.displayName ?? module;
  const rows = typeRows(saved.types, displayNameOf);

  const columns: DataTableColumn<TypeRow>[] = [
    {
      key: "type",
      header: "Notification type",
      render: (t) => (
        <span className="flex flex-col">
          <span>{t.label}</span>
          <span className="text-sm text-text-secondary">{t.moduleName}</span>
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
    ...CHANNELS.map(
      ({ channel, label, switchKey }): DataTableColumn<TypeRow> => ({
        key: channel,
        header: label,
        render: (t) =>
          t.availableChannels.includes(channel) ? (
            <Checkbox
              label={`${label} for ${t.label}`}
              labelHidden
              checked={effective(t).includes(channel)}
              disabled={defaultsLocked || !draft.channels[switchKey]}
              onChange={(on) => setTypeChannel(t, channel, on)}
            />
          ) : (
            <span className="text-text-secondary">
              <span aria-hidden="true">—</span>
              <span className="sr-only">Not available</span>
            </span>
          ),
      }),
    ),
    {
      key: "reset",
      header: "",
      render: (t) =>
        draft.defaults[t.type] !== undefined && !defaultsLocked ? (
          <Button variant="ghost" size="sm" onClick={() => resetType(t)} aria-label={`Reset ${t.label}`}>
            Reset
          </Button>
        ) : null,
    },
  ];

  const offChannels = CHANNELS.filter(({ switchKey }) => !draft.channels[switchKey]).map(({ label }) => label);

  return (
    <SettingsSection
      title="Notification delivery"
      description="Which channels your company's notifications can use, and what members get by default."
      dirty={dirty}
      saving={update.isPending}
      onSave={save}
      wide
    >
      <div className="flex max-w-xl flex-col gap-4">
        <div>
          <h3 className="font-medium text-sm text-text">Channels</h3>
          <p className="mt-1 text-sm text-text-secondary">
            Turning a channel off stops every notification on it for your whole company, whatever members have chosen
            and whatever a module sends. These are not defaults.
          </p>
        </div>
        {CHANNELS.map(({ label, switchKey }) => {
          const field = SWITCH_FIELDS[switchKey];
          return (
            <FieldWrapper
              key={switchKey}
              label={label}
              description={isLocked(field) ? LOCKED_NOTE : undefined}
              error={errorFor(error, field)}
            >
              <ToggleField
                value={draft.channels[switchKey]}
                onChange={(on) => change((d) => ({ ...d, channels: { ...d.channels, [switchKey]: on } }))}
                disabled={isLocked(field)}
              />
            </FieldWrapper>
          );
        })}
      </div>

      <div className="max-w-xl">
        <FieldWrapper
          label="SMS sender ID"
          description={
            isLocked("sms.sender_id")
              ? LOCKED_NOTE
              : "The name or number recipients see: up to 11 letters and digits, or a phone number such as +233201234567."
          }
          error={errorFor(error, "sms.sender_id")}
        >
          <TextInput
            value={draft.senderId}
            onChange={(v) => change((d) => ({ ...d, senderId: v }))}
            disabled={isLocked("sms.sender_id")}
          />
        </FieldWrapper>
      </div>

      <div className="flex flex-col gap-3">
        <div>
          <h3 className="font-medium text-sm text-text">Default channels per notification type</h3>
          <p className="mt-1 text-sm text-text-secondary">
            What a member gets for each type until they choose for themselves. A member's own choices always win.
            {offChannels.length > 0 &&
              ` ${offChannels.join(", ")} ${offChannels.length === 1 ? "is" : "are"} off above, so ${offChannels.length === 1 ? "its" : "their"} defaults have no effect.`}
          </p>
          {defaultsLocked && <p className="mt-1 text-sm text-text-secondary">{LOCKED_NOTE}</p>}
          {(error?.field === "defaults" || error?.field.startsWith("defaults.")) && (
            <p role="alert" className="mt-1 text-danger text-sm">
              {error.message}
            </p>
          )}
        </div>
        {rows.length === 0 ? (
          <p className="text-sm text-text-secondary">None of your installed modules send notifications.</p>
        ) : (
          <DataTable columns={columns} data={rows} keyExtractor={(t) => t.type} />
        )}
      </div>
    </SettingsSection>
  );
}
