import { createFileRoute } from "@tanstack/react-router";
import { AdminRoleDetailPage } from "../../admin/roles/admin-role-detail-page.js";

// shell-ux.md §5.2 "Role detail page".
export const Route = createFileRoute("/admin/roles/$roleId")({
  staticData: { breadcrumb: "Role" },
  component: AdminRoleRoute,
});

function AdminRoleRoute() {
  const { roleId } = Route.useParams();
  const navigate = Route.useNavigate();
  return (
    <AdminRoleDetailPage
      key={roleId}
      roleId={roleId}
      onBackToList={() => void navigate({ to: "/admin/roles" })}
      onOpenUsers={(role) => void navigate({ to: "/admin/users", search: { role } })}
    />
  );
}
