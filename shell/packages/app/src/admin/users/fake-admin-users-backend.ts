import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";

// An in-memory stand-in for the tenant admin user endpoints, installed over
// apiClient by the admin users tests and stories so every action really
// round-trips. The wire shapes and status codes match internal/engine/auth/
// adminusers.

export interface FakeUser {
  id: string;
  name: string | null;
  email: string;
  roles: string[];
  status: "active" | "invited" | "suspended" | "pending_verification";
  lastLoginAt: string | null;
  phone?: string | null;
  invitation?: { id: string; role: string; expiresAt: string; createdAt: string } | null;
}

export interface FakeSession {
  id: string;
  userAgent: string | null;
  ipAddress: string | null;
  countryCode: string | null;
  signedInAt: string;
  lastActiveAt: string;
  current?: boolean;
}

export interface FakeBackendOptions {
  users: FakeUser[];
  sessions?: Record<string, FakeSession[]>;
  // Emails with a GoERP account in another tenant (existing_account: true).
  otherTenantAccounts?: string[];
  // Every request fails with a 500.
  failAll?: boolean;
}

export interface FakeBackend {
  users: () => FakeUser[];
  sessions: (userId: string) => FakeSession[];
  requests: string[];
  restore: () => void;
}

function notFound(): AppError {
  return new AppError({ code: "not_found", message: "not found", httpStatus: 404 });
}

function userWire(user: FakeUser) {
  return {
    id: user.id,
    name: user.name,
    avatar_url: null,
    email: user.email,
    roles: user.status === "invited" ? [] : user.roles,
    status: user.status,
    last_login_at: user.lastLoginAt,
    invitation_id: user.status === "invited" ? (user.invitation?.id ?? null) : null,
  };
}

export function installFakeAdminUsersBackend(options: FakeBackendOptions): FakeBackend {
  const client = apiClient as unknown as Record<string, unknown>;
  const original = { get: client.get, post: client.post, delete: client.delete };
  let users = options.users.map((user) => ({ ...user, roles: [...user.roles] }));
  const sessions: Record<string, FakeSession[]> = Object.fromEntries(
    Object.entries(options.sessions ?? {}).map(([id, list]) => [id, [...list]]),
  );
  const requests: string[] = [];
  let nextId = 1000;

  const find = (id: string) => {
    const user = users.find((candidate) => candidate.id === id);
    if (!user) throw notFound();
    return user;
  };
  const member = (id: string) => {
    const user = find(id);
    if (user.status === "invited") throw notFound();
    return user;
  };
  const update = (id: string, patch: Partial<FakeUser>) => {
    users = users.map((user) => (user.id === id ? { ...user, ...patch } : user));
  };
  const guard = (method: string, path: string) => {
    requests.push(`${method} ${path}`);
    if (options.failAll) {
      throw new AppError({ code: "internal_error", message: "request failed", httpStatus: 500 });
    }
  };

  client.get = async (path: string, request?: { params?: Record<string, unknown> }) => {
    guard("GET", path);
    const params = request?.params ?? {};
    if (path === "/admin/users") {
      const q = String(params.q ?? "").toLowerCase();
      const status = params.status as string | undefined;
      const limit = Number(params.limit ?? 50);
      const matching = users
        .filter((user) => !q || user.email.includes(q) || (user.name ?? "").toLowerCase().includes(q))
        .filter((user) => !status || user.status === status)
        .sort((a, b) => a.email.localeCompare(b.email));
      const after = params.cursor ? atob(String(params.cursor)) : "";
      const page = matching.filter((user) => user.email > after).slice(0, limit);
      const last = page.at(-1);
      return {
        data: page.map(userWire),
        meta: { total: matching.length, cursor: page.length === limit && last ? btoa(last.email) : null },
      };
    }
    const sessionsMatch = path.match(/^\/admin\/users\/([^/]+)\/sessions$/);
    if (sessionsMatch?.[1]) {
      member(sessionsMatch[1]);
      return {
        sessions: (sessions[sessionsMatch[1]] ?? []).map((session) => ({
          id: session.id,
          user_agent: session.userAgent,
          ip_address: session.ipAddress,
          country_code: session.countryCode,
          signed_in_at: session.signedInAt,
          last_active_at: session.lastActiveAt,
          persistent: true,
          current: session.current ?? false,
        })),
      };
    }
    const detailMatch = path.match(/^\/admin\/users\/([^/]+)$/);
    if (detailMatch?.[1]) {
      const user = find(detailMatch[1]);
      const invitation = user.status === "invited" ? user.invitation : null;
      return {
        ...userWire(user),
        phone: user.phone ?? null,
        invitation: invitation
          ? {
              id: invitation.id,
              role: invitation.role,
              expires_at: invitation.expiresAt,
              created_at: invitation.createdAt,
            }
          : null,
      };
    }
    throw notFound();
  };

  client.post = async (path: string, body?: Record<string, string>) => {
    guard("POST", path);
    if (path === "/users/invite") {
      const email = (body?.email ?? "").trim().toLowerCase();
      if (!/^[^@\s]+@[^@\s]+$/.test(email)) {
        throw new AppError({ code: "invalid_email", message: "a valid email address is required", httpStatus: 400 });
      }
      const existing = users.find((user) => user.email === email);
      if (existing && existing.status !== "invited") {
        throw new AppError({ code: "already_member", message: "already a member", httpStatus: 409 });
      }
      const now = new Date();
      const invitation = {
        id: existing?.invitation?.id ?? `inv${nextId++}`,
        role: body?.role ?? "user",
        expiresAt: new Date(now.getTime() + 7 * 24 * 3600 * 1000).toISOString(),
        createdAt: now.toISOString(),
      };
      if (existing) {
        update(existing.id, { invitation });
      } else {
        users = [
          ...users,
          {
            id: `u${nextId++}`,
            name: body?.name || null,
            email,
            roles: [],
            status: "invited",
            lastLoginAt: null,
            invitation,
          },
        ];
      }
      return {
        invitation_id: invitation.id,
        expires_at: invitation.expiresAt,
        existing_account: (options.otherTenantAccounts ?? []).includes(email),
      };
    }
    const resendMatch = path.match(/^\/users\/invitations\/([^/]+)\/resend$/);
    if (resendMatch) {
      const user = users.find((candidate) => candidate.invitation?.id === resendMatch[1]);
      if (!user?.invitation) throw notFound();
      const invitation = { ...user.invitation, expiresAt: new Date(Date.now() + 7 * 24 * 3600 * 1000).toISOString() };
      update(user.id, { invitation });
      return { invitation_id: invitation.id, expires_at: invitation.expiresAt };
    }
    const actionMatch = path.match(/^\/admin\/users\/([^/]+)\/(suspend|unsuspend|roles)$/);
    if (actionMatch?.[1]) {
      const user = member(actionMatch[1]);
      const action = actionMatch[2];
      if (action === "suspend") {
        if (user.status !== "active") {
          throw new AppError({ code: "user_not_active", message: "not active", httpStatus: 409 });
        }
        update(user.id, { status: "suspended" });
        sessions[user.id] = [];
      } else if (action === "unsuspend") {
        if (user.status !== "suspended") {
          throw new AppError({ code: "user_not_suspended", message: "not suspended", httpStatus: 409 });
        }
        update(user.id, { status: "active" });
      } else if (body?.role && !user.roles.includes(body.role)) {
        update(user.id, { roles: [...user.roles, body.role].sort() });
      }
      return action === "roles" ? { status: "ok" } : undefined;
    }
    throw notFound();
  };

  client.delete = async (path: string) => {
    guard("DELETE", path);
    const roleMatch = path.match(/^\/admin\/users\/([^/]+)\/roles\/([^/]+)$/);
    if (roleMatch?.[1] && roleMatch[2]) {
      const user = member(roleMatch[1]);
      const role = decodeURIComponent(roleMatch[2]);
      update(user.id, { roles: user.roles.filter((candidate) => candidate !== role) });
      return { status: "ok" };
    }
    const sessionMatch = path.match(/^\/admin\/users\/([^/]+)\/sessions\/([^/]+)$/);
    if (sessionMatch?.[1]) {
      const list = sessions[sessionMatch[1]] ?? [];
      if (!list.some((session) => session.id === sessionMatch[2])) {
        throw new AppError({ code: "session_not_found", message: "session not found", httpStatus: 404 });
      }
      sessions[sessionMatch[1]] = list.filter((session) => session.id !== sessionMatch[2]);
      return undefined;
    }
    const userMatch = path.match(/^\/admin\/users\/([^/]+)$/);
    if (userMatch?.[1]) {
      const user = member(userMatch[1]);
      users = users.filter((candidate) => candidate.id !== user.id);
      sessions[user.id] = [];
      return undefined;
    }
    throw notFound();
  };

  return {
    users: () => users,
    sessions: (userId) => sessions[userId] ?? [],
    requests,
    restore: () => {
      Object.assign(client, original);
    },
  };
}
