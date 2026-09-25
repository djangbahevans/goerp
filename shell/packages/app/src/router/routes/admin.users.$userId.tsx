import { createFileRoute } from "@tanstack/react-router";
import { AdminUserDetailPage } from "../../admin/users/admin-user-detail-page.js";

// shell-ux.md §5.1 "User detail page".
export const Route = createFileRoute("/admin/users/$userId")({
  staticData: { breadcrumb: "User" },
  component: AdminUserRoute,
});

function AdminUserRoute() {
  const { userId } = Route.useParams();
  const navigate = Route.useNavigate();
  return (
    <AdminUserDetailPage key={userId} userId={userId} onBackToList={() => void navigate({ to: "/admin/users" })} />
  );
}
