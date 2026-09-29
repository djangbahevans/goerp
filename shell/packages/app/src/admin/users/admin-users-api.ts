import { apiClient } from "@goerp/sdk";
import { type InfiniteData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

// The tenant admin user endpoints (auth-internals.md §2, §3 "Invite flow",
// §4 "Session management endpoints", §17 "Tenant admin activity read API"),
// mapped from their snake_case wire shapes.

export type AdminUserStatus = "active" | "invited" | "suspended" | "pending_verification";
export type AdminUserStatusFilter = "all" | "active" | "invited" | "suspended";

export interface AdminUser {
  id: string;
  name: string | null;
  avatarUrl: string | null;
  email: string;
  roles: string[];
  status: AdminUserStatus;
  lastLoginAt: string | null;
  invitationId: string | null;
}

export interface AdminInvitation {
  id: string;
  role: string;
  expiresAt: string;
  createdAt: string;
}

export interface AdminUserDetail extends AdminUser {
  phone: string | null;
  invitation: AdminInvitation | null;
}

export interface AdminUserSession {
  id: string;
  userAgent: string | null;
  ipAddress: string | null;
  countryCode: string | null;
  signedInAt: string;
  lastActiveAt: string;
  persistent: boolean;
  current: boolean;
}

export type AdminActivitySource = "auth" | "data";
export type AdminActivityFilter = "all" | AdminActivitySource;

export interface AdminActivityPerson {
  id: string;
  name: string | null;
}

export interface AdminActivityEntry {
  id: string;
  source: AdminActivitySource;
  occurredAt: string;
  action: string;
  success: boolean;
  failureReason: string | null;
  actor: AdminActivityPerson | null;
  user: AdminActivityPerson | null;
  record: { model: string | null; id: string } | null;
  changedFields: string[] | null;
  metadata: Record<string, unknown> | null;
}

export interface AdminActivityPage {
  entries: AdminActivityEntry[];
  cursor: string | null;
}

export interface AdminUsersPage {
  users: AdminUser[];
  total: number;
  cursor: string | null;
}

export interface InviteUserInput {
  email: string;
  name: string;
  role: string;
}

export interface InviteUserResult {
  invitationId: string;
  existingAccount: boolean;
}

interface UserWire {
  id: string;
  name: string | null;
  avatar_url: string | null;
  email: string;
  roles: string[];
  status: AdminUserStatus;
  last_login_at: string | null;
  invitation_id: string | null;
}

interface UserDetailWire extends UserWire {
  phone: string | null;
  invitation: { id: string; role: string; expires_at: string; created_at: string } | null;
}

interface SessionWire {
  id: string;
  user_agent: string | null;
  ip_address: string | null;
  country_code: string | null;
  signed_in_at: string;
  last_active_at: string;
  persistent: boolean;
  current: boolean;
}

interface ActivityWire {
  id: string;
  source: AdminActivitySource;
  occurred_at: string;
  action: string;
  success: boolean;
  failure_reason: string | null;
  actor: AdminActivityPerson | null;
  user: AdminActivityPerson | null;
  record: { model: string | null; id: string } | null;
  changed_fields: string[] | null;
  metadata: Record<string, unknown> | null;
}

function toUser(wire: UserWire): AdminUser {
  return {
    id: wire.id,
    name: wire.name,
    avatarUrl: wire.avatar_url,
    email: wire.email,
    roles: wire.roles,
    status: wire.status,
    lastLoginAt: wire.last_login_at,
    invitationId: wire.invitation_id,
  };
}

function toUserDetail(wire: UserDetailWire): AdminUserDetail {
  return {
    ...toUser(wire),
    phone: wire.phone,
    invitation: wire.invitation && {
      id: wire.invitation.id,
      role: wire.invitation.role,
      expiresAt: wire.invitation.expires_at,
      createdAt: wire.invitation.created_at,
    },
  };
}

function toSession(wire: SessionWire): AdminUserSession {
  return {
    id: wire.id,
    userAgent: wire.user_agent,
    ipAddress: wire.ip_address,
    countryCode: wire.country_code,
    signedInAt: wire.signed_in_at,
    lastActiveAt: wire.last_active_at,
    persistent: wire.persistent,
    current: wire.current,
  };
}

function toActivity(wire: ActivityWire): AdminActivityEntry {
  return {
    id: wire.id,
    source: wire.source,
    occurredAt: wire.occurred_at,
    action: wire.action,
    success: wire.success,
    failureReason: wire.failure_reason,
    actor: wire.actor,
    user: wire.user,
    record: wire.record,
    changedFields: wire.changed_fields,
    metadata: wire.metadata,
  };
}

export const ADMIN_USERS_PAGE_SIZE = 50;
export const ADMIN_ACTIVITY_PAGE_SIZE = 20;

const adminUsersKey = ["admin-users"] as const;

export const adminUserKeys = {
  all: adminUsersKey,
  list: (search: string, status: AdminUserStatusFilter, role: string) =>
    [...adminUsersKey, "list", { search, status, role }] as const,
  detail: (id: string) => [...adminUsersKey, "detail", id] as const,
  sessions: (id: string) => [...adminUsersKey, "sessions", id] as const,
  activity: (id: string, filter: AdminActivityFilter) => [...adminUsersKey, "activity", id, filter] as const,
};

export function useAdminUsers(search: string, status: AdminUserStatusFilter, role: string) {
  return useInfiniteQuery<AdminUsersPage, Error, InfiniteData<AdminUsersPage>, readonly unknown[], string | null>({
    queryKey: adminUserKeys.list(search, status, role),
    initialPageParam: null,
    queryFn: async ({ pageParam, signal }) => {
      const { data, meta } = await apiClient.get<{ data: UserWire[]; meta: { total: number; cursor: string | null } }>(
        "/admin/users",
        {
          params: {
            limit: ADMIN_USERS_PAGE_SIZE,
            ...(search ? { q: search } : {}),
            ...(status !== "all" ? { status } : {}),
            ...(role ? { role } : {}),
            ...(pageParam ? { cursor: pageParam } : {}),
          },
          signal,
        },
      );
      return { users: data.map(toUser), total: meta.total, cursor: meta.cursor };
    },
    getNextPageParam: (last) => last.cursor,
  });
}

export function useAdminUser(id: string) {
  return useQuery({
    queryKey: adminUserKeys.detail(id),
    queryFn: async ({ signal }) => toUserDetail(await apiClient.get<UserDetailWire>(`/admin/users/${id}`, { signal })),
  });
}

export function useAdminUserSessions(id: string, enabled: boolean) {
  return useQuery({
    queryKey: adminUserKeys.sessions(id),
    enabled,
    queryFn: async ({ signal }) => {
      const { sessions } = await apiClient.get<{ sessions: SessionWire[] }>(`/admin/users/${id}/sessions`, { signal });
      return sessions.map(toSession);
    },
  });
}

export function useAdminUserActivity(id: string, filter: AdminActivityFilter) {
  return useInfiniteQuery<AdminActivityPage, Error, InfiniteData<AdminActivityPage>, readonly unknown[], string | null>(
    {
      queryKey: adminUserKeys.activity(id, filter),
      initialPageParam: null,
      queryFn: async ({ pageParam, signal }) => {
        const { data, meta } = await apiClient.get<{ data: ActivityWire[]; meta: { cursor: string | null } }>(
          `/admin/users/${id}/activity`,
          {
            params: {
              limit: ADMIN_ACTIVITY_PAGE_SIZE,
              ...(filter !== "all" ? { source: filter } : {}),
              ...(pageParam ? { cursor: pageParam } : {}),
            },
            signal,
          },
        );
        return { entries: data.map(toActivity), cursor: meta.cursor };
      },
      getNextPageParam: (last) => last.cursor,
    },
  );
}

// Every mutation refreshes all admin-user queries: a status or role change
// shows up in both the detail page and whichever list pages are cached.
function useAdminUserMutation<TInput, TResult>(mutationFn: (input: TInput) => Promise<TResult>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: adminUserKeys.all }),
  });
}

export function useInviteUser() {
  return useAdminUserMutation(async (input: InviteUserInput): Promise<InviteUserResult> => {
    const result = await apiClient.post<{ invitation_id: string; existing_account: boolean }>("/users/invite", {
      email: input.email,
      role: input.role,
      ...(input.name ? { name: input.name } : {}),
    });
    return { invitationId: result.invitation_id, existingAccount: result.existing_account };
  });
}

export function useResendInvitation() {
  return useAdminUserMutation((invitationId: string) =>
    apiClient.post<unknown>(`/users/invitations/${invitationId}/resend`),
  );
}

export function useSuspendUser(id: string) {
  return useAdminUserMutation((reason: string) => apiClient.post<void>(`/admin/users/${id}/suspend`, { reason }));
}

export function useUnsuspendUser(id: string) {
  return useAdminUserMutation<void, void>(() => apiClient.post<void>(`/admin/users/${id}/unsuspend`));
}

// Refreshes only the lists: refetching the deleted user's own detail would
// flash a 404 on the page that's about to navigate away.
export function useDeleteUser(id: string) {
  const queryClient = useQueryClient();
  return useMutation<void, Error, void>({
    mutationFn: () => apiClient.delete<void>(`/admin/users/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: [...adminUsersKey, "list"] }),
  });
}

export function useAssignRole(id: string) {
  return useAdminUserMutation((role: string) => apiClient.post<unknown>(`/admin/users/${id}/roles`, { role }));
}

export function useRevokeRole(id: string) {
  return useAdminUserMutation((role: string) =>
    apiClient.delete<unknown>(`/admin/users/${id}/roles/${encodeURIComponent(role)}`),
  );
}

export function useRevokeSession(id: string) {
  return useAdminUserMutation((familyId: string) => apiClient.delete<void>(`/admin/users/${id}/sessions/${familyId}`));
}
