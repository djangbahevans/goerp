import { createFileRoute } from "@tanstack/react-router";
import { ModuleDetailPage } from "../../admin/modules/module-detail-page.js";

// shell-ux.md §5.3 "Module detail page".
export const Route = createFileRoute("/admin/modules/$name")({
  staticData: { breadcrumb: "Module" },
  component: ModuleRoute,
});

function ModuleRoute() {
  const { name } = Route.useParams();
  const navigate = Route.useNavigate();
  return (
    <ModuleDetailPage
      key={name}
      name={name}
      onBackToList={() => void navigate({ to: "/admin/modules" })}
      onOpenModule={(dep) => void navigate({ to: "/admin/modules/$name", params: { name: dep } })}
    />
  );
}
