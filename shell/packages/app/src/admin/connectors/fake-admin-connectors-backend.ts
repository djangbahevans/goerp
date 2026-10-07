import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import type { ConfigOption } from "../config/config-api.js";

// An in-memory stand-in for the tenant admin connector endpoints, installed
// over apiClient by the connector tests and stories so every action really
// round-trips. Requests it doesn't own fall through to whatever apiClient did
// before. The wire shapes and status codes match
// internal/engine/auth/adminconnectors, and it never returns an encrypted
// value's plaintext.

export interface FakeConfigEntry {
  key: string;
  label: string;
  description?: string;
  type: string;
  fieldType?: string;
  category?: string;
  required?: boolean;
  options?: ConfigOption[];
  min?: number;
  max?: number;
  encrypted?: boolean;
  generated?: boolean;
  restartRequired?: boolean;
  default?: unknown;
  // The stored value, plaintext even for an encrypted entry. Absent when the
  // admin has set nothing.
  stored?: unknown;
}

export interface FakeConnector {
  name: string;
  displayName: string;
  description?: string;
  version?: string;
  enabled?: boolean;
  // The provider category, such as "sms_provider", when it is single-active.
  category?: string;
  config: FakeConfigEntry[];
  // Answers GET /connectors/{name}/status; absent means the connector has no
  // status route.
  status?: Record<string, unknown> | { fail: string };
  // Whether the connector verifies inbound webhooks, so a complete save
  // mints its endpoint token.
  webhooks?: boolean;
  // The active endpoint token, minted by the first complete save.
  webhookToken?: string;
}

export interface FakeConnectorsBackendOptions {
  connectors: FakeConnector[];
  // The module name currently primary for each category.
  primary?: Record<string, string>;
  // Every /admin/connectors and /admin/config request fails with a 500.
  failAll?: boolean;
}

export interface FakeConnectorsBackend {
  connectors: () => FakeConnector[];
  requests: { method: string; path: string; body?: unknown }[];
  restore: () => void;
}

const MASK = "***";

function fail(code: string, httpStatus: number, details?: Record<string, unknown>): AppError {
  return new AppError({ code, message: code.replaceAll("_", " "), httpStatus, details: details ?? null });
}

type Handler = (path: string, ...args: unknown[]) => Promise<unknown>;

function isSet(entry: FakeConfigEntry): boolean {
  return entry.stored !== undefined;
}

function entryWire(entry: FakeConfigEntry) {
  const shown = entry.encrypted ? (isSet(entry) ? MASK : null) : isSet(entry) ? entry.stored : (entry.default ?? null);
  return {
    key: entry.key,
    label: entry.label,
    ...(entry.description ? { description: entry.description } : {}),
    type: entry.type,
    ...(entry.fieldType ? { field_type: entry.fieldType } : {}),
    ...(entry.category ? { category: entry.category } : {}),
    required: entry.required ?? false,
    ...(entry.options ? { options: entry.options } : {}),
    ...(entry.min !== undefined ? { min: entry.min } : {}),
    ...(entry.max !== undefined ? { max: entry.max } : {}),
    encrypted: entry.encrypted ?? false,
    generated: entry.generated ?? false,
    restart_required: entry.restartRequired ?? false,
    default: entry.default ?? null,
    is_set: isSet(entry),
    value: shown,
  };
}

function validate(entry: FakeConfigEntry, value: unknown): string | null {
  if (entry.options && entry.options.length > 0) {
    const items = Array.isArray(value) ? value : [value];
    if (items.some((item) => !entry.options?.some((o) => o.value === String(item))))
      return "not one of the allowed options";
  }
  switch (entry.type) {
    case "integer":
      if (typeof value !== "number" || !Number.isInteger(value)) return "must be a whole number";
      break;
    case "float":
      if (typeof value !== "number") return "must be a number";
      break;
    case "boolean":
      if (typeof value !== "boolean") return "must be a boolean";
      break;
    case "string":
      if (typeof value !== "string") return "must be a string";
      break;
  }
  if (typeof value === "number") {
    if (entry.min !== undefined && value < entry.min) return `must be at least ${entry.min}`;
    if (entry.max !== undefined && value > entry.max) return `must be at most ${entry.max}`;
  }
  return null;
}

export function installFakeAdminConnectorsBackend(options: FakeConnectorsBackendOptions): FakeConnectorsBackend {
  const client = apiClient as unknown as Record<string, Handler>;
  const original = { get: client.get, post: client.post, patch: client.patch, delete: client.delete };
  let connectors = options.connectors.map((c) => ({ ...c, config: c.config.map((e) => ({ ...e })) }));
  const primary = { ...options.primary };
  const requests: FakeConnectorsBackend["requests"] = [];

  const record = (method: string, path: string, body?: unknown) => {
    requests.push({ method, path, ...(body === undefined ? {} : { body }) });
    if (options.failAll && !path.startsWith("/connectors/")) throw fail("internal_error", 500);
  };
  const find = (name: string) => {
    const connector = connectors.find((c) => c.name === name);
    if (!connector) throw fail("not_found", 404);
    return connector;
  };
  const providersOf = (category: string) => connectors.filter((c) => c.category === category && c.enabled !== false);
  const configured = (c: FakeConnector) =>
    c.config.every((e) => !e.required || isSet(e) || (e.default !== undefined && e.default !== null));
  const summary = (c: FakeConnector) => {
    const peers = c.category ? providersOf(c.category) : [];
    const resolved = c.category
      ? (primary[c.category] ?? (peers.length === 1 ? peers[0]?.name : undefined))
      : undefined;
    return {
      name: c.name,
      display_name: c.displayName,
      ...(c.description ? { description: c.description } : {}),
      version: c.version ?? "1.0.0",
      enabled: c.enabled ?? true,
      configured: configured(c),
      has_status_route: c.status !== undefined,
      provider: c.category
        ? { category: c.category, primary: resolved === c.name, can_set_primary: peers.length > 1 }
        : null,
    };
  };

  client.get = async (path, ...rest) => {
    if (path.startsWith("/connectors/") && path.endsWith("/status")) {
      record("GET", path);
      const connector = find(path.split("/")[2] ?? "");
      if (!connector.status) throw fail("route_not_found", 404);
      if ("fail" in connector.status) throw fail(String(connector.status.fail), 502);
      return connector.status;
    }
    if (path !== "/admin/connectors" && !path.startsWith("/admin/connectors/"))
      return original.get?.call(apiClient, path, ...rest);
    record("GET", path);
    if (path === "/admin/connectors") {
      return { connectors: [...connectors].sort((a, b) => a.displayName.localeCompare(b.displayName)).map(summary) };
    }
    const connector = find(path.slice("/admin/connectors/".length));
    return {
      ...summary(connector),
      ...(connector.webhookToken ? { webhook_path: `/_webhooks/${connector.name}/${connector.webhookToken}` } : {}),
      config: connector.config.map(entryWire),
    };
  };

  client.patch = async (path, ...rest) => {
    const body = (rest[0] ?? {}) as Record<string, unknown>;
    if (path.startsWith("/admin/connectors/") && path.endsWith("/set-primary")) {
      record("PATCH", path);
      const connector = find(path.split("/")[3] ?? "");
      if (!connector.category) throw fail("not_a_provider", 422);
      primary[connector.category] = connector.name;
      return { module_name: connector.name, category: connector.category };
    }
    if (path !== "/admin/config") return original.patch?.call(apiClient, path, ...rest);
    record("PATCH", path, body);

    const names = new Set(Object.keys(body).map((qualified) => qualified.split(".")[0]));
    if (names.size > 1)
      throw fail(
        "invalid_config",
        422,
        Object.fromEntries(Object.keys(body).map((k) => [k, "one module per request"])),
      );
    const connector = find([...names][0] ?? "");
    const problems: Record<string, string> = {};
    const writes: [FakeConfigEntry, unknown][] = [];
    for (const [qualified, value] of Object.entries(body)) {
      const entry = connector.config.find((e) => e.key === qualified.slice(connector.name.length + 1));
      if (!entry) problems[qualified] = "not a declared config key";
      else if (entry.generated) problems[qualified] = "a generated value cannot be written directly; rotate it instead";
      else if (value === null) {
        if (entry.required && entry.default == null)
          problems[qualified] = "a required value with no default cannot be cleared";
        else writes.push([entry, undefined]);
      } else if (!(entry.encrypted && value === MASK)) {
        const problem = validate(entry, value);
        if (problem) problems[qualified] = problem;
        else writes.push([entry, value]);
      }
    }
    if (Object.keys(problems).length > 0) throw fail("invalid_config", 422, problems);
    for (const [entry, value] of writes) {
      if (value === undefined) delete entry.stored;
      else entry.stored = value;
    }
    if (connector.webhooks && configured(connector) && !connector.webhookToken) {
      connector.webhookToken = `token${requests.length}`;
    }
    return {
      module_name: connector.name,
      updated: writes.map(([entry]) => entry.key),
      restart_required: writes.filter(([entry]) => entry.restartRequired).map(([entry]) => entry.key),
      configured: configured(connector),
    };
  };

  client.post = async (path, ...rest) => {
    const match = /^\/admin\/connectors\/([^/]+)\/config\/([^/]+)\/rotate$/.exec(path);
    if (!match) return original.post?.call(apiClient, path, ...rest);
    record("POST", path);
    const connector = find(match[1] ?? "");
    const entry = connector.config.find((e) => e.key === match[2]);
    if (!entry) throw fail("not_found", 404);
    if (!entry.generated || entry.type !== "string") throw fail("not_rotatable", 422);
    entry.stored = `rotated-${requests.length}`;
    return { module_name: connector.name, key: entry.key, value: entry.encrypted ? MASK : entry.stored };
  };

  client.delete = async (path, ...rest) => {
    const match = /^\/admin\/connectors\/([^/]+)\/webhook$/.exec(path);
    if (!match) return original.delete?.call(apiClient, path, ...rest);
    record("DELETE", path);
    const connector = find(match[1] ?? "");
    if (!connector.webhookToken) throw fail("no_webhook_endpoint", 404);
    delete connector.webhookToken;
    return undefined;
  };

  return {
    connectors: () => connectors,
    requests,
    restore: () => {
      Object.assign(client, original);
      connectors = [];
    },
  };
}
