import { createFileRoute } from "@tanstack/react-router";
import { ModulesPage } from "../../admin/modules/modules-page.js";

// shell-ux.md §5.3 "Modules".
export const Route = createFileRoute("/admin/modules/")({
  staticData: { breadcrumb: "Modules" },
  component: ModulesRoute,
});

function ModulesRoute() {
  const navigate = Route.useNavigate();
  return <ModulesPage onOpenModule={(name) => void navigate({ to: "/admin/modules/$name", params: { name } })} />;
}
