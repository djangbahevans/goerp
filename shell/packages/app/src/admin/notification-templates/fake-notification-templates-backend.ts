import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import {
  CHANNEL_COLUMNS,
  type TemplateChannel,
  type TemplateFields,
  type VariableType,
} from "./notification-templates-api.js";

// An in-memory stand-in for /admin/settings/notification-templates,
// installed over apiClient by the notification templates tests and
// stories. Requests outside its own paths fall through to whatever
// apiClient did before. Wire shapes, status codes and the SMS length rules
// match internal/engine/auth/adminsettings/notiftemplates.go; its renderer
// only substitutes {{.Name}} actions and rejects an unclosed one.

export interface FakeTemplateType {
  type: string;
  module: string;
  label: string;
  availableChannels: TemplateChannel[];
  dataSchema?: Record<string, VariableType>;
}

export interface FakeTemplate {
  type: string;
  channel: TemplateChannel;
  locale: string;
  default?: TemplateFields;
  override?: TemplateFields;
}

export interface FakeNotificationTemplatesOptions {
  types: FakeTemplateType[];
  templates: FakeTemplate[];
  // Every request fails with a 500.
  failAll?: boolean;
  // Every save fails with a 500.
  failSave?: boolean;
  // The tenant's email layout doesn't render: an email preview is a 422 invalid_setting.
  brokenLayout?: boolean;
  // Each preview response is held back this long.
  previewDelayMs?: number;
}

export interface FakeNotificationTemplatesBackend {
  templates: () => FakeTemplate[];
  requests: { method: string; path: string; body?: unknown }[];
  restore: () => void;
}

const BASE = "/admin/settings/notification-templates";
const ENGINE_VARIABLES = ["TenantName", "TenantLogoURL", "UserName", "UserFirstName", "ActionURL", "UnsubscribeURL"];
const GSM =
  "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà\f^{}\\[~]|€";

function fail(code: string, httpStatus: number, details?: Record<string, unknown>): AppError {
  return new AppError({ code, message: code.replaceAll("_", " "), httpStatus, details: details ?? null });
}

function checkParses(column: string, source: string): void {
  if (source.split("{{").length !== source.split("}}").length) {
    throw fail("invalid_template", 422, { field: column, message: `template: ${column}:1: unclosed action` });
  }
}

function render(source: string, vars: Record<string, unknown>): string {
  return source.replaceAll(/\{\{\s*\.(\w+)\s*\}\}/g, (_, name: string) => String(vars[name] ?? ""));
}

function smsLength(text: string) {
  const characters = [...text].length;
  const gsm = [...text].every((ch) => GSM.includes(ch));
  const [single, multi] = gsm ? [160, 153] : [70, 67];
  return { characters, segments: characters > single ? Math.ceil(characters / multi) : 1 };
}

type Handler = (path: string, ...args: unknown[]) => Promise<unknown>;

export function installFakeNotificationTemplatesBackend(
  options: FakeNotificationTemplatesOptions,
): FakeNotificationTemplatesBackend {
  const client = apiClient as unknown as Record<string, Handler>;
  const original = { get: client.get, put: client.put, post: client.post, delete: client.delete };
  let templates = options.templates.map((t) => ({ ...t }));
  const requests: FakeNotificationTemplatesBackend["requests"] = [];

  const ours = (path: string) => path === BASE || path.startsWith(`${BASE}/`);
  const record = (method: string, path: string, body?: unknown) => {
    requests.push({ method, path, ...(body === undefined ? {} : { body }) });
    if (options.failAll) throw fail("internal_error", 500);
  };

  const target = (path: string) => {
    const [type = "", channel = "", locale = ""] = path
      .slice(BASE.length + 1)
      .replace(/\/preview$/, "")
      .split("/")
      .map(decodeURIComponent);
    const declared = options.types.find((t) => t.type === type);
    if (!declared?.availableChannels.includes(channel as TemplateChannel)) throw fail("not_found", 404);
    return { declared, channel: channel as TemplateChannel, locale };
  };
  const find = (type: string, channel: string, locale: string) =>
    templates.find((t) => t.type === type && t.channel === channel && t.locale === locale);

  const templateWire = (declared: FakeTemplateType, channel: TemplateChannel, locale: string) => {
    const row = find(declared.type, channel, locale);
    return {
      type: declared.type,
      channel,
      locale,
      default: row?.default ?? null,
      override: row?.override ?? null,
      variables: [
        ...Object.entries(declared.dataSchema ?? {})
          .toSorted(([a], [b]) => a.localeCompare(b))
          .map(([name, type]) => ({ name, type, source: "data" })),
        ...ENGINE_VARIABLES.map((name) => ({ name, type: "string", source: "engine" })),
        { name: "Year", type: "int", source: "engine" },
      ],
    };
  };

  const fields = (channel: TemplateChannel, body: Record<string, string>): TemplateFields => {
    const out: TemplateFields = {};
    for (const [column, source] of Object.entries(body)) {
      if (!(CHANNEL_COLUMNS[channel] as string[]).includes(column)) throw fail("invalid_request", 400);
      if (source !== "") out[column as keyof TemplateFields] = source;
    }
    if (Object.keys(out).length === 0) throw fail("invalid_request", 400);
    for (const [column, source] of Object.entries(out)) checkParses(column, source);
    return out;
  };

  client.get = async (path, ...rest) => {
    if (!ours(path)) return original.get?.call(apiClient, path, ...rest);
    record("GET", path);
    if (path === BASE) {
      return {
        types: options.types.map((declared) => ({
          type: declared.type,
          module: declared.module,
          label: declared.label,
          available_channels: declared.availableChannels,
          templates: templates
            .filter((t) => t.type === declared.type && (t.default || t.override))
            .map((t) => ({
              channel: t.channel,
              locale: t.locale,
              customised: t.override !== undefined,
              has_default: t.default !== undefined,
            })),
        })),
      };
    }
    const { declared, channel, locale } = target(path);
    return templateWire(declared, channel, locale);
  };

  client.put = async (path, ...rest) => {
    if (!ours(path)) return original.put?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as Record<string, string>;
    record("PUT", path, body);
    if (options.failSave) throw fail("internal_error", 500);
    const { declared, channel, locale } = target(path);
    const override = fields(channel, body);
    const row = find(declared.type, channel, locale);
    templates = row
      ? templates.map((t) => (t === row ? { ...t, override } : t))
      : [...templates, { type: declared.type, channel, locale, override }];
    return templateWire(declared, channel, locale);
  };

  client.delete = async (path, ...rest) => {
    if (!ours(path)) return original.delete?.call(apiClient, path, ...rest);
    record("DELETE", path);
    const { declared, channel, locale } = target(path);
    templates = templates
      .map((t): FakeTemplate => {
        if (t.type !== declared.type || t.channel !== channel || t.locale !== locale) return t;
        const { override: _deleted, ...rest } = t;
        return rest;
      })
      .filter((t) => t.default || t.override);
    return templateWire(declared, channel, locale);
  };

  client.post = async (path, ...rest) => {
    if (!ours(path)) return original.post?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as { template?: Record<string, string>; data?: Record<string, unknown> };
    record("POST", path, body);
    const { declared, channel } = target(path);
    const draft = fields(channel, body.template ?? {});
    const vars: Record<string, unknown> = {
      ...Object.fromEntries(
        Object.entries(declared.dataSchema ?? {}).map(([name, type]) => [
          name,
          type === "int" ? 1 : type === "float" ? 1.5 : type === "bool" ? true : name,
        ]),
      ),
      ...body.data,
      TenantName: "Acme",
      UserName: "Ada Admin",
      UserFirstName: "Ada",
      ActionURL: "https://acme.example/",
      UnsubscribeURL: "https://acme.example/_notif/unsubscribe?token=preview",
      Year: 2026,
    };
    const rendered: TemplateFields = {};
    for (const [column, source] of Object.entries(draft))
      rendered[column as keyof TemplateFields] = render(source, vars);
    if (rendered.html_template !== undefined) {
      if (options.brokenLayout) throw fail("invalid_setting", 422, { field: "email.layout_template" });
      rendered.html_template = `<!doctype html><html><body style="font-family:sans-serif">${rendered.html_template}<p style="color:#666">© 2026 Acme</p></body></html>`;
    }
    if (rendered.sms_template !== undefined) rendered.sms_template = rendered.sms_template.trim();
    if (options.previewDelayMs) await new Promise((resolve) => setTimeout(resolve, options.previewDelayMs));
    return {
      rendered,
      ...(rendered.sms_template !== undefined ? { sms: smsLength(rendered.sms_template) } : {}),
    };
  };

  return {
    templates: () => templates,
    requests,
    restore: () => {
      Object.assign(client, original);
    },
  };
}
