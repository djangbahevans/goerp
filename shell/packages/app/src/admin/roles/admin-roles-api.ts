import { apiClient } from "@goerp/sdk";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminUserKeys } from "../users/admin-users-api.js";

// The tenant admin role endpoints (auth-internals.md §10 "Tenant admin role
// endpoints"), mapped from their snake_case wire shapes.

export interface AdminRole {
  id: string;
  name: string;
  description: string | null;
  isImmutable: boolean;
  userCount: number;
  invitationCount: number;
}

export interface AdminRoleDetail extends AdminRole {
  permissions: string[];
}

export interface CatalogPermission {
  name: string;
  description: string;
  category: string;
  module: string;
}

export interface RoleInput {
  name: string;
  description: string;
  permissions: string[];
}

interface RoleWire {
  id: string;
  name: string;
  description: string | null;
  is_immutable: boolean;
  user_count: number;
  invitation_count: number;
}

interface RoleDetailWire extends RoleWire {
  permissions: string[];
}

function toRole(wire: RoleWire): AdminRole {
  return {
    id: wire.id,
    name: wire.name,
    description: wire.description,
    isImmutable: wire.is_immutable,
    userCount: wire.user_count,
    invitationCount: wire.invitation_count,
  };
}

function toRoleDetail(wire: RoleDetailWire): AdminRoleDetail {
  return { ...toRole(wire), permissions: wire.permissions };
}

const adminRolesKey = ["admin-roles"] as const;

export const adminRoleKeys = {
  all: adminRolesKey,
  list: () => [...adminRolesKey, "list"] as const,
  detail: (id: string) => [...adminRolesKey, "detail", id] as const,
  catalog: () => [...adminRolesKey, "catalog"] as const,
};

export function useAdminRoles() {
  return useQuery({
    queryKey: adminRoleKeys.list(),
    queryFn: async ({ signal }) => {
      const { data } = await apiClient.get<{ data: RoleWire[] }>("/admin/roles", { signal });
      return data.map(toRole);
    },
  });
}

export function useAdminRole(id: string) {
  return useQuery({
    queryKey: adminRoleKeys.detail(id),
    queryFn: async ({ signal }) => toRoleDetail(await apiClient.get<RoleDetailWire>(`/admin/roles/${id}`, { signal })),
  });
}

export function usePermissionCatalog() {
  return useQuery({
    queryKey: adminRoleKeys.catalog(),
    queryFn: async ({ signal }) => {
      const { permissions } = await apiClient.get<{ permissions: CatalogPermission[] }>("/admin/roles/permissions", {
        signal,
      });
      return permissions;
    },
  });
}

// A rename changes the role names the user directory shows, so role writes
// refresh the admin-user queries too.
function useRoleMutation<TInput, TResult>(mutationFn: (input: TInput) => Promise<TResult>, refreshDetail = true) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: refreshDetail ? adminRoleKeys.all : adminRoleKeys.list() }),
        queryClient.invalidateQueries({ queryKey: adminUserKeys.all }),
      ]);
    },
  });
}

export function useCreateRole() {
  return useRoleMutation(async (input: RoleInput) =>
    toRoleDetail(
      await apiClient.post<RoleDetailWire>("/admin/roles", {
        name: input.name,
        permissions: input.permissions,
        ...(input.description ? { description: input.description } : {}),
      }),
    ),
  );
}

export function useUpdateRole(id: string) {
  return useRoleMutation(async (input: Partial<RoleInput>) =>
    toRoleDetail(await apiClient.patch<RoleDetailWire>(`/admin/roles/${id}`, input)),
  );
}

// Refreshes only the list: refetching the deleted role's own detail would
// flash a 404 on the page that's about to navigate away.
export function useDeleteRole(id: string) {
  return useRoleMutation<void, void>(() => apiClient.delete<void>(`/admin/roles/${id}`), false);
}
