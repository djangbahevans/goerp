import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";

// An in-memory stand-in for the admin activity-types endpoints and the
// public GET /_meta/activity-types, installed over apiClient by the admin
// activity-types tests and stories. Requests outside its own paths fall
// through to whatever apiClient did before, so it can sit on top of other
// fakes. Wire shapes and status codes match internal/engine/dispatch_
// activity_types.go and scheduled-activities.md §9 "API".

export interface FakeActivityType {
  key: string;
  label: Record<string, string>;
  icon: string;
  defaultSummary?: Record<string, string>;
  defaultDueDays?: number | null;
  archived?: boolean;
  usageCount?: number;
}

export interface FakeActivityTypesBackendOptions {
  types: FakeActivityType[];
  locale?: string;
  // Every request fails with a 500.
  failAll?: boolean;
}

export interface FakeActivityTypesBackend {
  types: () => FakeActivityType[];
  requests: { method: string; path: string; body?: unknown }[];
  restore: () => void;
}

const KEY_PATTERN = /^[a-z][a-z0-9_]{0,39}$/;
const MAX_TYPES = 50;

function fail(code: string, httpStatus: number): AppError {
  return new AppError({ code, message: code.replaceAll("_", " "), httpStatus });
}

function resolve(map: Record<string, string>, locale: string, defaultLocale: string): string | null {
  return map[locale] ?? map[defaultLocale] ?? Object.values(map)[0] ?? null;
}

function publicWire(type: FakeActivityType, locale: string, defaultLocale: string) {
  return {
    key: type.key,
    label: resolve(type.label, locale, defaultLocale) ?? type.key,
    icon: type.icon,
    default_summary: type.defaultSummary ? resolve(type.defaultSummary, locale, defaultLocale) : null,
    default_due_days: type.defaultDueDays ?? null,
    archived: type.archived ?? false,
  };
}

function adminWire(type: FakeActivityType) {
  return {
    key: type.key,
    label: type.label,
    icon: type.icon,
    default_summary: type.defaultSummary ?? {},
    default_due_days: type.defaultDueDays ?? null,
    archived: type.archived ?? false,
    usage_count: type.usageCount ?? 0,
  };
}

type Handler = (path: string, ...args: unknown[]) => Promise<unknown>;

export function installFakeAdminActivityTypesBackend(
  options: FakeActivityTypesBackendOptions,
): FakeActivityTypesBackend {
  const client = apiClient as unknown as Record<string, Handler>;
  const original = { get: client.get, post: client.post, put: client.put, patch: client.patch, delete: client.delete };
  const locale = options.locale ?? "en";
  let types = options.types.map((type) => ({ ...type }));
  const requests: FakeActivityTypesBackend["requests"] = [];

  const ours = (path: string) =>
    path === "/_meta/activity-types" || path === "/admin/activity-types" || path.startsWith("/admin/activity-types/");
  const record = (method: string, path: string, body?: unknown) => {
    requests.push({ method, path, ...(body === undefined ? {} : { body }) });
    if (options.failAll) throw fail("internal_error", 500);
  };
  const find = (path: string) => {
    const key = path.slice("/admin/activity-types/".length);
    const type = types.find((candidate) => candidate.key === key);
    if (!type) throw fail("not_found", 404);
    return type;
  };
  const defaultLocale = types[0]?.label ? Object.keys(types[0].label)[0] : locale;

  client.get = async (path, ...rest) => {
    if (!ours(path)) return original.get?.call(apiClient, path, ...rest);
    record("GET", path);
    if (path === "/_meta/activity-types") {
      return { data: types.map((type) => publicWire(type, locale, defaultLocale ?? locale)) };
    }
    return { data: types.map(adminWire) };
  };

  client.post = async (path, ...rest) => {
    if (!ours(path)) return original.post?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as {
      key?: string;
      label?: Record<string, string>;
      icon?: string;
      default_summary?: Record<string, string>;
      default_due_days?: number;
    };
    record("POST", path, body);
    if (path !== "/admin/activity-types") throw fail("not_found", 404);
    if (types.length >= MAX_TYPES) throw fail("type_limit_reached", 409);
    const key = body.key ?? "";
    if (!KEY_PATTERN.test(key) || key === "order") throw fail("invalid_request", 400);
    if (types.some((t) => t.key === key)) throw fail("type_key_taken", 409);
    if (!body.label || !body.icon) throw fail("invalid_request", 400);
    const type: FakeActivityType = {
      key,
      label: body.label,
      icon: body.icon,
      defaultSummary: body.default_summary ?? {},
      defaultDueDays: body.default_due_days ?? null,
      archived: false,
      usageCount: 0,
    };
    types = [...types, type];
    return adminWire(type);
  };

  client.patch = async (path, ...rest) => {
    if (!ours(path)) return original.patch?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as {
      label?: Record<string, string>;
      icon?: string;
      default_summary?: Record<string, string>;
      default_due_days?: number | null;
      archived?: boolean;
    };
    record("PATCH", path, body);
    const type = find(path);
    if (body.archived === true) {
      const activeCount = types.filter((t) => !t.archived).length;
      if (!type.archived && activeCount <= 1) throw fail("last_active_type", 409);
    }
    const updated: FakeActivityType = {
      ...type,
      ...(body.label !== undefined ? { label: body.label } : {}),
      ...(body.icon !== undefined ? { icon: body.icon } : {}),
      ...(body.default_summary !== undefined ? { defaultSummary: body.default_summary } : {}),
      ...(body.default_due_days !== undefined ? { defaultDueDays: body.default_due_days } : {}),
      ...(body.archived !== undefined ? { archived: body.archived } : {}),
    };
    types = types.map((candidate) => (candidate.key === type.key ? updated : candidate));
    return adminWire(updated);
  };

  client.put = async (path, ...rest) => {
    if (!ours(path)) return original.put?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as { keys?: string[] };
    record("PUT", path, body);
    if (path !== "/admin/activity-types/order") throw fail("not_found", 404);
    const keys = body.keys ?? [];
    const currentKeys = new Set(types.map((t) => t.key));
    if (keys.length !== types.length || !keys.every((key) => currentKeys.has(key))) throw fail("invalid_request", 400);
    types = keys.map((key) => types.find((t) => t.key === key)) as FakeActivityType[];
    return { data: types.map(adminWire) };
  };

  client.delete = async (path, ...rest) => {
    if (!ours(path)) return original.delete?.call(apiClient, path, ...rest);
    record("DELETE", path);
    const type = find(path);
    if ((type.usageCount ?? 0) > 0) throw fail("type_in_use", 409);
    types = types.filter((candidate) => candidate.key !== type.key);
    return undefined;
  };

  return {
    types: () => types,
    requests,
    restore: () => {
      Object.assign(client, original);
    },
  };
}
