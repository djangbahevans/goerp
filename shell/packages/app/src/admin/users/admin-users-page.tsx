import {
  ActionButton,
  Button,
  DataTable,
  type DataTableColumn,
  EmptyState,
  formatRelativeTime,
  Icon,
  PageHeader,
  PageLayout,
  TabPanel,
  Tabs,
  TextInput,
  UserAvatar,
} from "@goerp/sdk/components";
import { type ReactNode, useEffect, useState } from "react";
import { type AdminUser, type AdminUserStatusFilter, useAdminUsers } from "./admin-users-api.js";
import { InviteUserSheet } from "./invite-user-sheet.js";
import { roleLabel } from "./roles.js";
import { UserStatusBadge } from "./user-status-badge.js";

const SEARCH_DEBOUNCE_MS = 300;

export const STATUS_TABS: { id: AdminUserStatusFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "active", label: "Active" },
  { id: "invited", label: "Invited" },
  { id: "suspended", label: "Suspended" },
];

const EMPTY_TITLES: Record<AdminUserStatusFilter, string> = {
  all: "No users yet",
  active: "No active users",
  invited: "No pending invitations",
  suspended: "No suspended users",
};

export function displayName(user: Pick<AdminUser, "name" | "email">): string {
  return user.name || user.email;
}

const COLUMNS: DataTableColumn<AdminUser>[] = [
  {
    key: "name",
    header: "Name",
    render: (user) => (
      <span className="flex items-center gap-2">
        <UserAvatar userId={user.id} name={displayName(user)} avatarUrl={user.avatarUrl} size="sm" />
        <span className="font-medium text-text">{user.name || "—"}</span>
      </span>
    ),
  },
  { key: "email", header: "Email", render: (user) => user.email },
  { key: "roles", header: "Roles", render: (user) => user.roles.map(roleLabel).join(", ") || "—" },
  { key: "status", header: "Status", render: (user) => <UserStatusBadge status={user.status} /> },
  { key: "lastLogin", header: "Last login", render: (user) => formatRelativeTime(user.lastLoginAt, "Never") },
];

export interface AdminUsersPageProps {
  search: string;
  status: AdminUserStatusFilter;
  onSearchChange: (search: string) => void;
  onStatusChange: (status: AdminUserStatusFilter) => void;
  onOpenUser: (id: string) => void;
}

// shell-ux.md §5.1 "Users". The search and status tab live in the URL
// (the route owns them) so returning from a user's page restores the list.
export function AdminUsersPage({
  search,
  status,
  onSearchChange,
  onStatusChange,
  onOpenUser,
}: AdminUsersPageProps): ReactNode {
  const [inviteOpen, setInviteOpen] = useState(false);
  const [draft, setDraft] = useState(search);

  // Only an outside change (back/forward, a link) resyncs the box; the
  // debounced write of this box's own trimmed text must leave its spaces.
  useEffect(() => setDraft((current) => (current.trim() === search ? current : search)), [search]);
  useEffect(() => {
    const trimmed = draft.trim();
    if (trimmed === search) return;
    const timer = setTimeout(() => onSearchChange(trimmed), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [draft, search, onSearchChange]);

  return (
    <PageLayout>
      <PageHeader
        title="Users"
        actions={
          <Button variant="primary" onClick={() => setInviteOpen(true)}>
            Invite user
          </Button>
        }
      />
      <div className="flex flex-col gap-4">
        <div className="max-w-sm">
          <TextInput
            type="search"
            value={draft}
            onChange={setDraft}
            placeholder="Search by name or email"
            aria-label="Search users"
            start={<Icon name="search" size={16} className="text-text-secondary" aria-hidden="true" />}
          />
        </div>
        <Tabs items={STATUS_TABS} activeId={status} onChange={(id) => onStatusChange(id as AdminUserStatusFilter)}>
          {STATUS_TABS.map((tab) => (
            <TabPanel key={tab.id} id={tab.id}>
              <UsersTable search={search} status={tab.id} onOpenUser={onOpenUser} />
            </TabPanel>
          ))}
        </Tabs>
      </div>
      <InviteUserSheet open={inviteOpen} onClose={() => setInviteOpen(false)} />
    </PageLayout>
  );
}

interface UsersTableProps {
  search: string;
  status: AdminUserStatusFilter;
  onOpenUser: (id: string) => void;
}

function UsersTable({ search, status, onOpenUser }: UsersTableProps): ReactNode {
  const query = useAdminUsers(search, status);
  const users = query.data?.pages.flatMap((page) => page.users) ?? [];
  const total = query.data?.pages[0]?.total ?? 0;

  if (query.isError) {
    return (
      <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
        <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
        <p className="text-text">Couldn't load users.</p>
        <ActionButton variant="secondary" onClick={() => void query.refetch()}>
          Retry
        </ActionButton>
      </div>
    );
  }

  const empty = search ? (
    <EmptyState icon="search-x" title="No users found" description={`Nothing matches "${search}".`} />
  ) : (
    <EmptyState icon="users" title={EMPTY_TITLES[status]} />
  );

  return (
    <div className="flex flex-col gap-3 pt-4">
      <DataTable
        columns={COLUMNS}
        data={users}
        keyExtractor={(user) => user.id}
        onRowClick={(user) => onOpenUser(user.id)}
        isLoading={query.isLoading}
        emptyState={empty}
      />
      {!query.isLoading && users.length > 0 && (
        <p className="text-sm text-text-secondary">
          Showing {users.length} of {total}
        </p>
      )}
      {query.hasNextPage && (
        <div className="flex justify-center">
          <ActionButton
            variant="secondary"
            loading={query.isFetchingNextPage}
            onClick={() => void query.fetchNextPage()}
          >
            Load more
          </ActionButton>
        </div>
      )}
    </div>
  );
}
