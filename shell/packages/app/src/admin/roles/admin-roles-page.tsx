import {
  ActionButton,
  Badge,
  Button,
  DataTable,
  type DataTableColumn,
  EmptyState,
  Icon,
  PageHeader,
  PageLayout,
} from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { roleLabel } from "../users/roles.js";
import { type AdminRole, useAdminRoles } from "./admin-roles-api.js";

function RoleName({ role }: { role: Pick<AdminRole, "name" | "isImmutable"> }): ReactNode {
  return (
    <span className="flex items-center gap-2">
      <span className="font-medium text-text">{roleLabel(role.name)}</span>
      {role.isImmutable && <Badge label="System" color="gray" />}
    </span>
  );
}

export function userCountLabel(count: number): string {
  return count === 1 ? "1 user" : `${count} users`;
}

const COLUMNS: DataTableColumn<AdminRole>[] = [
  { key: "name", header: "Name", render: (role) => <RoleName role={role} /> },
  { key: "description", header: "Description", render: (role) => role.description || "—" },
  { key: "users", header: "Users", render: (role) => userCountLabel(role.userCount) },
];

export interface AdminRolesPageProps {
  onOpenRole: (id: string) => void;
  onCreateRole: () => void;
}

// shell-ux.md §5.2 "Roles".
export function AdminRolesPage({ onOpenRole, onCreateRole }: AdminRolesPageProps): ReactNode {
  const query = useAdminRoles();

  return (
    <PageLayout>
      <PageHeader
        title="Roles"
        actions={
          <Button variant="primary" onClick={onCreateRole}>
            Create role
          </Button>
        }
      />
      {query.isError ? (
        <div role="alert" className="flex flex-col items-center gap-2 py-6 text-center">
          <Icon name="circle-alert" size={20} className="text-danger" aria-hidden="true" />
          <p className="text-text">Couldn't load roles.</p>
          <ActionButton variant="secondary" onClick={() => void query.refetch()}>
            Retry
          </ActionButton>
        </div>
      ) : (
        <DataTable
          columns={COLUMNS}
          data={query.data ?? []}
          keyExtractor={(role) => role.id}
          onRowClick={(role) => onOpenRole(role.id)}
          isLoading={query.isLoading}
          emptyState={<EmptyState icon="shield" title="No roles yet" />}
        />
      )}
    </PageLayout>
  );
}
