import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import type { CatalogPermission } from "./admin-roles-api.js";

// An in-memory stand-in for the tenant admin role endpoints, installed over
// apiClient by the admin roles tests and stories so every action really
// round-trips. Requests outside /admin/roles fall through to whatever
// apiClient did before, so it can sit on top of the admin users fake. The
// wire shapes and status codes match internal/engine/auth/adminroles.

export interface FakeRole {
  id: string;
  name: string;
  description: string | null;
  isImmutable: boolean;
  userCount: number;
  invitationCount?: number;
  permissions: string[];
}

export interface FakeRolesBackendOptions {
  roles: FakeRole[];
  catalog: CatalogPermission[];
  // Every /admin/roles request fails with a 500.
  failAll?: boolean;
}

export interface FakeRolesBackend {
  roles: () => FakeRole[];
  requests: { method: string; path: string; body?: unknown }[];
  restore: () => void;
}

const NAME_PATTERN = /^[a-z][a-z0-9_]{0,62}$/;

function fail(code: string, httpStatus: number): AppError {
  return new AppError({ code, message: code.replaceAll("_", " "), httpStatus });
}

function wire(role: FakeRole) {
  return {
    id: role.id,
    name: role.name,
    description: role.description,
    is_immutable: role.isImmutable,
    user_count: role.userCount,
    invitation_count: role.invitationCount ?? 0,
  };
}

function detailWire(role: FakeRole) {
  return { ...wire(role), permissions: [...role.permissions].sort() };
}

type Handler = (path: string, ...args: unknown[]) => Promise<unknown>;

export function installFakeAdminRolesBackend(options: FakeRolesBackendOptions): FakeRolesBackend {
  const client = apiClient as unknown as Record<string, Handler>;
  const original = { get: client.get, post: client.post, patch: client.patch, delete: client.delete };
  let roles = options.roles.map((role) => ({ ...role, permissions: [...role.permissions] }));
  const catalogNames = new Set(options.catalog.map((p) => p.name));
  const requests: FakeRolesBackend["requests"] = [];
  let nextId = 1000;

  const ours = (path: string) => path === "/admin/roles" || path.startsWith("/admin/roles/");
  const record = (method: string, path: string, body?: unknown) => {
    requests.push({ method, path, ...(body === undefined ? {} : { body }) });
    if (options.failAll) throw fail("internal_error", 500);
  };
  const find = (path: string) => {
    const id = path.slice("/admin/roles/".length);
    const role = roles.find((candidate) => candidate.id === id);
    if (!role) throw fail("not_found", 404);
    return role;
  };
  const checkName = (name: string, self?: string) => {
    if (!NAME_PATTERN.test(name) || name === "superadmin" || name === "public") throw fail("invalid_name", 400);
    if (roles.some((role) => role.name === name && role.id !== self)) throw fail("role_name_taken", 409);
  };
  const checkPermissions = (names: string[], held: string[] = []) => {
    if (names.some((name) => !catalogNames.has(name) && !held.includes(name))) throw fail("unknown_permission", 400);
  };

  client.get = async (path, ...rest) => {
    if (!ours(path)) return original.get?.call(apiClient, path, ...rest);
    record("GET", path);
    if (path === "/admin/roles") {
      const sorted = [...roles].sort(
        (a, b) => Number(b.isImmutable) - Number(a.isImmutable) || a.name.localeCompare(b.name),
      );
      return { data: sorted.map(wire) };
    }
    if (path === "/admin/roles/permissions") return { permissions: options.catalog };
    return detailWire(find(path));
  };

  client.post = async (path, ...rest) => {
    if (!ours(path)) return original.post?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as { name?: string; description?: string; permissions?: string[] };
    record("POST", path, body);
    if (path !== "/admin/roles") throw fail("not_found", 404);
    const name = body.name ?? "";
    checkName(name);
    const permissions = [...new Set(body.permissions ?? [])];
    checkPermissions(permissions);
    const role: FakeRole = {
      id: `role-${nextId++}`,
      name,
      description: body.description?.trim() || null,
      isImmutable: false,
      userCount: 0,
      permissions,
    };
    roles = [...roles, role];
    return detailWire(role);
  };

  client.patch = async (path, ...rest) => {
    if (!ours(path)) return original.patch?.call(apiClient, path, ...rest);
    const body = (rest[0] ?? {}) as { name?: string; description?: string; permissions?: string[] };
    record("PATCH", path, body);
    const role = find(path);
    if (role.isImmutable) throw fail("role_immutable", 403);
    if (body.name !== undefined) checkName(body.name, role.id);
    if (body.permissions !== undefined) checkPermissions(body.permissions, role.permissions);
    const updated: FakeRole = {
      ...role,
      ...(body.name !== undefined ? { name: body.name } : {}),
      ...(body.description !== undefined ? { description: body.description.trim() || null } : {}),
      ...(body.permissions !== undefined ? { permissions: [...new Set(body.permissions)] } : {}),
    };
    roles = roles.map((candidate) => (candidate.id === role.id ? updated : candidate));
    return detailWire(updated);
  };

  client.delete = async (path, ...rest) => {
    if (!ours(path)) return original.delete?.call(apiClient, path, ...rest);
    record("DELETE", path);
    const role = find(path);
    if (role.isImmutable) throw fail("role_immutable", 403);
    if (role.userCount > 0 || (role.invitationCount ?? 0) > 0) throw fail("role_in_use", 409);
    roles = roles.filter((candidate) => candidate.id !== role.id);
    return undefined;
  };

  return {
    roles: () => roles,
    requests,
    restore: () => {
      Object.assign(client, original);
    },
  };
}
