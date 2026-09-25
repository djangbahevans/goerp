import { createFileRoute } from "@tanstack/react-router";
import { useCallback } from "react";
import type { AdminUserStatusFilter } from "../../admin/users/admin-users-api.js";
import { AdminUsersPage, STATUS_TABS } from "../../admin/users/admin-users-page.js";

export interface AdminUsersSearch {
  q?: string;
  status?: AdminUserStatusFilter;
  role?: string;
}

function isStatusFilter(value: unknown): value is AdminUserStatusFilter {
  return STATUS_TABS.some((tab) => tab.id === value);
}

// The defaults ("" and "all") stay out of the URL.
function toSearch(q: string, status: AdminUserStatusFilter, role: string): AdminUsersSearch {
  return { ...(q ? { q } : {}), ...(status !== "all" ? { status } : {}), ...(role ? { role } : {}) };
}

// shell-ux.md §5.1 "Users".
export const Route = createFileRoute("/admin/users/")({
  staticData: { breadcrumb: "Users" },
  validateSearch: (search: Record<string, unknown>): AdminUsersSearch =>
    toSearch(
      typeof search.q === "string" ? search.q : "",
      isStatusFilter(search.status) ? search.status : "all",
      typeof search.role === "string" ? search.role : "",
    ),
  component: AdminUsersRoute,
});

function AdminUsersRoute() {
  const search = Route.useSearch();
  const q = search.q ?? "";
  const status = search.status ?? "all";
  const navigate = Route.useNavigate();
  const onSearchChange = useCallback(
    (next: string) =>
      void navigate({ search: (prev) => toSearch(next, prev.status ?? "all", prev.role ?? ""), replace: true }),
    [navigate],
  );
  return (
    <AdminUsersPage
      search={q}
      status={status}
      roleFilter={search.role}
      onSearchChange={onSearchChange}
      onStatusChange={(next) =>
        void navigate({ search: (prev) => toSearch(prev.q ?? "", next, prev.role ?? ""), replace: true })
      }
      onClearRole={() => void navigate({ search: (prev) => toSearch(prev.q ?? "", prev.status ?? "all", "") })}
      onOpenUser={(userId) => void navigate({ to: "/admin/users/$userId", params: { userId } })}
    />
  );
}
