import { createFileRoute } from "@tanstack/react-router";
import { AdminCreateRolePage } from "../../admin/roles/admin-role-detail-page.js";

// shell-ux.md §5.2: "Create role" reuses the role detail form.
export const Route = createFileRoute("/admin/roles/new")({
  staticData: { breadcrumb: "New role" },
  component: AdminNewRoleRoute,
});

function AdminNewRoleRoute() {
  const navigate = Route.useNavigate();
  return (
    <AdminCreateRolePage
      onCreated={(roleId) => void navigate({ to: "/admin/roles/$roleId", params: { roleId }, replace: true })}
    />
  );
}
