import { useTenant } from "@goerp/sdk/auth";
import {
  ActionButton,
  type ActionMenuItem,
  Badge,
  Button,
  Checkbox,
  EmptyState,
  Icon,
  PageHeader,
  PageLayout,
  Skeleton,
  TextInput,
} from "@goerp/sdk/components";
import { toast } from "@goerp/sdk/notifications";
import { ViewRegistryContext } from "@goerp/sdk/schema";
import { type ReactNode, useContext, useEffect, useRef, useState } from "react";
import { OverflowMenu } from "../../chrome/overflow-menu.js";
import { NotificationTemplateSheet, removalCopy } from "./notification-template-sheet.js";
import {
  type TemplateTarget,
  type TemplateType,
  useNotificationTemplates,
  useResetNotificationTemplate,
} from "./notification-templates-api.js";
import {
  CHANNELS,
  channelLabel,
  fallbackLocale,
  languageName,
  type TemplateRow,
  templateGroups,
} from "./template-rows.js";

const COLUMNS = 5;
const HEADER_CELL = "p-3 text-left font-medium text-sm text-text-secondary";

// shell-ux.md §5.8 "Notification templates".
export function AdminNotificationTemplatesPage(): ReactNode {
  const tenant = useTenant();
  const registry = useContext(ViewRegistryContext);
  const query = useNotificationTemplates();
  const reset = useResetNotificationTemplate();
  const [search, setSearch] = useState("");
  const [customisedOnly, setCustomisedOnly] = useState(false);
  const [sheet, setSheet] = useState<{ open: boolean; target: TemplateTarget | null; initialType?: string }>({
    open: false,
    target: null,
  });
  const [focusRows, setFocusRows] = useState<string[] | null>(null);
  const tableRef = useRef<HTMLTableElement>(null);

  const types = query.data ?? [];
  const groups = templateGroups(types, {
    displayNameOf: (module) => registry?.notificationTypes.find((g) => g.module === module)?.displayName ?? module,
    defaultLocale: tenant.defaultLocale,
    search,
    customisedOnly,
  });
  const rows = groups.flatMap((g) => g.rows);

  // A reset or delete can remove its row (a deleted template, or a reset
  // one under "Customised only"), taking the focused menu trigger with it.
  // Focus then moves to a neighbouring row's menu; the reset resolves after
  // the list reloads, so the table already reflects it here.
  useEffect(() => {
    if (!focusRows) return;
    const trigger = focusRows
      .map((key) => tableRef.current?.querySelector<HTMLElement>(`tr[data-row="${CSS.escape(key)}"] button`))
      .find((candidate) => candidate);
    trigger?.focus();
  }, [focusRows]);

  async function removeOverride(row: Extract<TemplateRow, { kind: "template" }>): Promise<void> {
    const index = rows.findIndex((r) => r.key === row.key);
    const neighbour = rows[index + 1] ?? rows[index - 1];
    const deleting = !row.entry.hasDefault;
    try {
      await reset.mutateAsync({
        target: { type: row.type.type, channel: row.entry.channel, locale: row.entry.locale },
      });
      toast.success(deleting ? "Template deleted." : "Template reset to the default.");
      setFocusRows([row.key, ...(neighbour ? [neighbour.key] : [])]);
    } catch {
      toast.error(deleting ? "Couldn't delete this template." : "Couldn't reset this template.");
    }
  }

  const filtered = search.trim() !== "" || customisedOnly;

  return (
    <PageLayout>
      <PageHeader
        title="Notification templates"
        subtitle="What your members' notifications say, for every type and language."
        actions={
          <Button variant="primary" onClick={() => setSheet({ open: true, target: null })}>
            Add template
          </Button>
        }
      />
      {query.isLoading ? (
        <Skeleton type="table" columns={COLUMNS} />
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load notification templates.</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-4">
            <div className="w-full max-w-xs">
              <TextInput
                type="search"
                aria-label="Search notifications"
                placeholder="Search notifications"
                value={search}
                onChange={setSearch}
              />
            </div>
            <Checkbox label="Customised only" checked={customisedOnly} onChange={setCustomisedOnly} />
          </div>
          {groups.length === 0 && filtered ? (
            <EmptyState
              icon="search-x"
              title="No templates match"
              action={
                <Button
                  onClick={() => {
                    setSearch("");
                    setCustomisedOnly(false);
                  }}
                >
                  Clear filters
                </Button>
              }
            />
          ) : (
            <div className="overflow-x-auto rounded-structural border border-border">
              <table ref={tableRef} className="w-full border-collapse">
                <thead>
                  <tr className="border-border border-b bg-surface">
                    <th scope="col" className={HEADER_CELL}>
                      Notification
                    </th>
                    <th scope="col" className={HEADER_CELL}>
                      Channel
                    </th>
                    <th scope="col" className={HEADER_CELL}>
                      Language
                    </th>
                    <th scope="col" className={HEADER_CELL}>
                      Status
                    </th>
                    <th scope="col" className="w-8 p-3">
                      <span className="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                {groups.map((group) => (
                  <tbody key={group.key}>
                    <tr className="border-border border-b bg-bg-subtle">
                      <th scope="rowgroup" colSpan={COLUMNS} className="p-3 text-left font-semibold text-sm text-text">
                        {group.name}
                      </th>
                    </tr>
                    {group.rows.map((row) => (
                      <TemplateTableRow
                        key={row.key}
                        row={row}
                        types={types}
                        onEdit={(target) => setSheet({ open: true, target })}
                        onAdd={(type) => setSheet({ open: true, target: null, initialType: type })}
                        onRemove={(r) => void removeOverride(r)}
                      />
                    ))}
                  </tbody>
                ))}
              </table>
            </div>
          )}
        </div>
      )}
      <NotificationTemplateSheet
        open={sheet.open}
        onClose={() => setSheet((current) => ({ ...current, open: false }))}
        target={sheet.target}
        initialType={sheet.initialType}
      />
    </PageLayout>
  );
}

interface TemplateTableRowProps {
  row: TemplateRow;
  types: TemplateType[];
  onEdit: (target: TemplateTarget) => void;
  onAdd: (type: string) => void;
  onRemove: (row: Extract<TemplateRow, { kind: "template" }>) => void;
}

function TemplateTableRow({ row, types, onEdit, onAdd, onRemove }: TemplateTableRowProps): ReactNode {
  const notificationCell = (
    <td className="p-3 align-top">
      <p className="text-sm text-text">{row.type.label}</p>
      <p className="font-mono text-text-secondary text-xs">{row.type.type}</p>
    </td>
  );

  if (row.kind === "empty") {
    return (
      <tr data-row={row.key} className="border-border border-b">
        {notificationCell}
        <td colSpan={3} className="p-3 text-sm text-text-secondary">
          No templates. Sent with its label as the title.
        </td>
        <td className="p-3 text-right">
          <OverflowMenu
            label={`${row.type.label} actions`}
            items={[{ label: "Add template", icon: "plus", onClick: () => onAdd(row.type.type) }]}
          />
        </td>
      </tr>
    );
  }

  const { entry, type } = row;
  const target = { type: type.type, channel: entry.channel, locale: entry.locale };
  const language = languageName(entry.locale);
  const channel = CHANNELS.find((c) => c.channel === entry.channel);
  const items: ActionMenuItem[] = [{ label: "Edit", icon: "pencil", onClick: () => onEdit(target) }];
  if (entry.customised) {
    const copy = removalCopy(target, entry.hasDefault, fallbackLocale(types, type.type, entry.channel, entry.locale));
    items.push({
      label: entry.hasDefault ? "Reset to default" : "Delete template",
      icon: entry.hasDefault ? "rotate-ccw" : "trash-2",
      ...(entry.hasDefault ? {} : { variant: "danger" as const }),
      confirm: { title: copy.title, message: copy.description, confirmLabel: copy.confirmLabel, destructive: true },
      onClick: () => onRemove(row),
    });
  }

  return (
    <tr data-row={row.key} className="border-border border-b">
      {notificationCell}
      <td className="p-3 align-top text-sm text-text">
        <span className="flex items-center gap-2">
          {channel && <Icon name={channel.icon} size={16} className="text-text-secondary" aria-hidden="true" />}
          {channelLabel(entry.channel)}
        </span>
      </td>
      <td className="p-3 align-top text-sm text-text">
        {language}
        {language !== entry.locale && (
          <span className="ms-2 font-mono text-text-secondary text-xs">{entry.locale}</span>
        )}
      </td>
      <td className="p-3 align-top text-sm">
        {entry.customised ? (
          <Badge label="Customised" color="blue" />
        ) : (
          <span className="text-text-secondary">Default</span>
        )}
      </td>
      <td className="p-3 text-right align-top">
        <OverflowMenu label={`${type.label}, ${channelLabel(entry.channel)}, ${language} actions`} items={items} />
      </td>
    </tr>
  );
}
