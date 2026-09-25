import { createFileRoute } from "@tanstack/react-router";
import { AdminRolesPage } from "../../admin/roles/admin-roles-page.js";

// shell-ux.md §5.2 "Roles".
export const Route = createFileRoute("/admin/roles/")({
  staticData: { breadcrumb: "Roles" },
  component: AdminRolesRoute,
});

function AdminRolesRoute() {
  const navigate = Route.useNavigate();
  return (
    <AdminRolesPage
      onOpenRole={(roleId) => void navigate({ to: "/admin/roles/$roleId", params: { roleId } })}
      onCreateRole={() => void navigate({ to: "/admin/roles/new" })}
    />
  );
}
