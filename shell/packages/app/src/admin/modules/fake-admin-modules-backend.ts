import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";

// An in-memory stand-in for the tenant admin module endpoints, installed
// over apiClient by the module tests and stories so a toggle really
// round-trips. Requests it doesn't own fall through to whatever apiClient did
// before. The wire shapes, statuses and error codes match
// internal/engine/auth/adminmodules.

export interface FakeConfigEntry {
  key: string;
  label: string;
  description?: string;
  type: string;
  fieldType?: string;
  category?: string;
  options?: { value: string; label: string }[];
  encrypted?: boolean;
  default?: unknown;
  // The stored value, plaintext even for an encrypted entry. Absent when the
  // admin has set nothing.
  stored?: unknown;
}

export interface FakeModule {
  name: string;
  displayName: string;
  description?: string;
  version?: string;
  type?: string;
  // The plan includes the module. Defaults to true.
  entitled?: boolean;
  // Disabled by the tenant. Defaults to false.
  disabled?: boolean;
  dependsOn?: string[];
  permissions?: { name: string; description?: string; category?: string }[];
  config?: FakeConfigEntry[];
}

export interface FakeModulesBackendOptions {
  modules: FakeModule[];
  // Every /admin/modules request fails with a 500.
  failAll?: boolean;
  // Only a module's detail read, which carries its config, fails with a 500.
  failConfig?: boolean;
}

export interface FakeModulesBackend {
  modules: () => FakeModule[];
  requests: { method: string; path: string; body?: unknown }[];
  restore: () => void;
}

function fail(code: string, httpStatus: number, details?: Record<string, unknown>): AppError {
  return new AppError({ code, message: code.replaceAll("_", " "), httpStatus, details: details ?? null });
}

const isEnabled = (m: FakeModule) => (m.entitled ?? true) && !m.disabled;

function wire(m: FakeModule) {
  return {
    name: m.name,
    display_name: m.displayName,
    ...(m.description ? { description: m.description } : {}),
    version: m.version ?? "1.0.0",
    type: m.type ?? "domain",
    entitled: m.entitled ?? true,
    enabled: isEnabled(m),
    depends_on: m.dependsOn ?? [],
    permissions: m.permissions ?? [],
  };
}

const MASK = "***";

function entryWire(entry: FakeConfigEntry) {
  const isSet = entry.stored !== undefined;
  const value = entry.encrypted ? (isSet ? MASK : null) : isSet ? entry.stored : (entry.default ?? null);
  return {
    key: entry.key,
    label: entry.label,
    ...(entry.description ? { description: entry.description } : {}),
    type: entry.type,
    ...(entry.fieldType ? { field_type: entry.fieldType } : {}),
    ...(entry.category ? { category: entry.category } : {}),
    required: false,
    ...(entry.options ? { options: entry.options } : {}),
    encrypted: entry.encrypted ?? false,
    generated: false,
    restart_required: false,
    default: entry.default ?? null,
    is_set: isSet,
    value,
  };
}

export function installFakeAdminModulesBackend(options: FakeModulesBackendOptions): FakeModulesBackend {
  // Entries are copied too, so a save in one test never reaches the shared fixtures.
  const modules = options.modules.map((m) =>
    m.config ? { ...m, config: m.config.map((entry) => ({ ...entry })) } : { ...m },
  );
  const requests: FakeModulesBackend["requests"] = [];
  const client = apiClient as unknown as Record<string, (path: string, ...args: unknown[]) => Promise<unknown>>;
  const original = { get: client.get, patch: client.patch };

  const find = (name: string) => {
    const found = modules.find((m) => m.name === name);
    if (!found) throw fail("not_found", 404);
    return found;
  };

  client.get = async (path, ...rest) => {
    if (path !== "/admin/modules" && !path.startsWith("/admin/modules/"))
      return original.get?.call(apiClient, path, ...rest);
    requests.push({ method: "GET", path });
    if (options.failAll) throw fail("internal_error", 500);
    if (path === "/admin/modules") {
      return { modules: [...modules].sort((a, b) => a.displayName.localeCompare(b.displayName)).map(wire) };
    }
    if (options.failConfig) throw fail("internal_error", 500);
    const target = find(path.slice("/admin/modules/".length));
    return { ...wire(target), config: (target.entitled ?? true) ? (target.config ?? []).map(entryWire) : [] };
  };

  client.patch = async (path, ...rest) => {
    if (path === "/admin/config") {
      const body = (rest[0] ?? {}) as Record<string, unknown>;
      requests.push({ method: "PATCH", path, body });
      const names = new Set(Object.keys(body).map((qualified) => qualified.split(".")[0]));
      const target = find([...names][0] ?? "");
      const problems: Record<string, string> = {};
      const writes: [FakeConfigEntry, unknown][] = [];
      for (const [qualified, value] of Object.entries(body)) {
        const entry = target.config?.find((e) => e.key === qualified.slice(target.name.length + 1));
        if (!entry) problems[qualified] = "not a declared config key";
        else if (typeof value === "string" && value.length > 8) problems[qualified] = "must be at most 8 characters";
        else if (!(entry.encrypted && value === MASK)) writes.push([entry, value]);
      }
      if (Object.keys(problems).length > 0) throw fail("invalid_config", 422, problems);
      for (const [entry, value] of writes) {
        if (value === null) delete entry.stored;
        else entry.stored = value;
      }
      return { module_name: target.name, updated: writes.map(([entry]) => entry.key), configured: true };
    }
    const match = /^\/admin\/modules\/([^/]+)\/settings$/.exec(path);
    if (!match) return original.patch?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as { enabled?: unknown };
    requests.push({ method: "PATCH", path, body });
    if (options.failAll) throw fail("internal_error", 500);
    if (typeof body.enabled !== "boolean") throw fail("invalid_request", 400);

    const target = find(match[1] ?? "");
    if (!(target.entitled ?? true)) throw fail("module_not_entitled", 409);
    if (body.enabled !== isEnabled(target)) {
      if (body.enabled) {
        const off = (target.dependsOn ?? []).filter((dep) => !isEnabled(find(dep)));
        if (off.length > 0) throw fail("module_dependency_disabled", 409, { modules: off.sort() });
      } else {
        const dependents = modules
          .filter((m) => isEnabled(m) && (m.dependsOn ?? []).includes(target.name))
          .map((m) => m.name);
        if (dependents.length > 0) throw fail("module_has_dependents", 409, { modules: dependents.sort() });
      }
      target.disabled = !body.enabled;
    }
    return wire(target);
  };

  return {
    modules: () => modules,
    requests,
    restore: () => {
      client.get = original.get as (path: string, ...args: unknown[]) => Promise<unknown>;
      client.patch = original.patch as (path: string, ...args: unknown[]) => Promise<unknown>;
    },
  };
}
