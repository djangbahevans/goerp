import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";

// An in-memory stand-in for the tenant admin module endpoints, installed
// over apiClient by the module tests and stories so a toggle really
// round-trips. Requests it doesn't own fall through to whatever apiClient did
// before. The wire shapes, statuses and error codes match
// internal/engine/auth/adminmodules.

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
}

export interface FakeModulesBackendOptions {
  modules: FakeModule[];
  // Every /admin/modules request fails with a 500.
  failAll?: boolean;
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

export function installFakeAdminModulesBackend(options: FakeModulesBackendOptions): FakeModulesBackend {
  const modules = options.modules.map((m) => ({ ...m }));
  const requests: FakeModulesBackend["requests"] = [];
  const client = apiClient as unknown as Record<string, (path: string, ...args: unknown[]) => Promise<unknown>>;
  const original = { get: client.get, patch: client.patch };

  const find = (name: string) => {
    const found = modules.find((m) => m.name === name);
    if (!found) throw fail("not_found", 404);
    return found;
  };

  client.get = async (path, ...rest) => {
    if (path !== "/admin/modules") return original.get?.call(apiClient, path, ...rest);
    requests.push({ method: "GET", path });
    if (options.failAll) throw fail("internal_error", 500);
    return { modules: [...modules].sort((a, b) => a.displayName.localeCompare(b.displayName)).map(wire) };
  };

  client.patch = async (path, ...rest) => {
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
