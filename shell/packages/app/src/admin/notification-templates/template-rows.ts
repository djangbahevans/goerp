import type { TemplateChannel, TemplateColumn, TemplateEntry, TemplateType } from "./notification-templates-api.js";

export const ENGINE_MODULE = "engine";

export const CHANNELS: { channel: TemplateChannel; label: string; icon: string }[] = [
  { channel: "in_app", label: "In-app", icon: "bell" },
  { channel: "email", label: "Email", icon: "mail" },
  { channel: "sms", label: "SMS", icon: "message-square" },
  { channel: "push", label: "Push", icon: "smartphone" },
];

export function channelLabel(channel: TemplateChannel): string {
  return CHANNELS.find((c) => c.channel === channel)?.label ?? channel;
}

export const COLUMN_LABELS: Record<TemplateColumn, string> = {
  title_template: "Title",
  body_template: "Body",
  action_url_template: "Action URL",
  icon: "Icon",
  subject_template: "Subject",
  html_template: "HTML body",
  text_template: "Plain-text version",
  sms_template: "Message",
  push_title_template: "Title",
  push_body_template: "Body",
};

const LANGUAGE_NAMES = new Intl.DisplayNames(undefined, { type: "language" });

export function languageName(tag: string): string {
  try {
    return LANGUAGE_NAMES.of(tag) ?? tag;
  } catch {
    return tag;
  }
}

function hasTemplate(types: TemplateType[], type: string, channel: TemplateChannel, locale: string): boolean {
  return types.some((t) => t.type === type && t.templates.some((e) => e.channel === channel && e.locale === locale));
}

// The locale a member using `locale` gets when it has no template of its
// own (notification-system.md §5 "Locale fallback"): its base language,
// else en. null when neither has a template either.
export function fallbackLocale(
  types: TemplateType[],
  type: string,
  channel: TemplateChannel,
  locale: string,
): string | null {
  const [base] = locale.split("-");
  if (base && base !== locale && hasTemplate(types, type, channel, base)) return base;
  if (locale !== "en" && hasTemplate(types, type, channel, "en")) return "en";
  return null;
}

export type TemplateRow =
  | { kind: "template"; key: string; type: TemplateType; entry: TemplateEntry }
  | { kind: "empty"; key: string; type: TemplateType };

export interface TemplateGroup {
  key: string;
  name: string;
  rows: TemplateRow[];
}

export interface GroupOptions {
  displayNameOf: (module: string) => string;
  defaultLocale: string;
  search: string;
  customisedOnly: boolean;
}

export function rowKey(type: string, channel?: TemplateChannel, locale?: string): string {
  return channel && locale ? `${type}/${channel}/${locale}` : type;
}

// Engine types lead as "General", then modules by display name, the way
// NotificationDeliverySection orders them. Within a group, rows sort by
// type label, then channel, then language with the tenant's default first.
export function templateGroups(types: TemplateType[], options: GroupOptions): TemplateGroup[] {
  const query = options.search.trim().toLowerCase();
  const channelRank = (channel: TemplateChannel) => CHANNELS.findIndex((c) => c.channel === channel);
  const compareLocales = (a: string, b: string) =>
    Number(b === options.defaultLocale) - Number(a === options.defaultLocale) ||
    languageName(a).localeCompare(languageName(b));

  const groups = new Map<string, TemplateGroup>();
  const visible = types
    .filter((t) => query === "" || t.label.toLowerCase().includes(query) || t.type.toLowerCase().includes(query))
    .toSorted((a, b) => a.label.localeCompare(b.label));
  for (const type of visible) {
    const entries = type.templates
      .filter((e) => !options.customisedOnly || e.customised)
      .toSorted((a, b) => channelRank(a.channel) - channelRank(b.channel) || compareLocales(a.locale, b.locale));
    const rows: TemplateRow[] = entries.map((entry) => ({
      kind: "template",
      key: rowKey(type.type, entry.channel, entry.locale),
      type,
      entry,
    }));
    if (type.templates.length === 0 && !options.customisedOnly)
      rows.push({ kind: "empty", key: rowKey(type.type), type });
    if (rows.length === 0) continue;

    const isEngine = type.module === ENGINE_MODULE;
    const group = groups.get(type.module) ?? {
      key: type.module,
      name: isEngine ? "General" : options.displayNameOf(type.module),
      rows: [],
    };
    group.rows.push(...rows);
    groups.set(type.module, group);
  }

  return [...groups.values()].toSorted(
    (a, b) => Number(b.key === ENGINE_MODULE) - Number(a.key === ENGINE_MODULE) || a.name.localeCompare(b.name),
  );
}
