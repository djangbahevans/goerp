import { createFileRoute } from "@tanstack/react-router";
import { ConnectorsPage } from "../../admin/connectors/connectors-page.js";

// shell-ux.md §5.4 "Connectors".
export const Route = createFileRoute("/admin/connectors/")({
  staticData: { breadcrumb: "Connectors" },
  component: ConnectorsRoute,
});

function ConnectorsRoute() {
  const navigate = Route.useNavigate();
  return (
    <ConnectorsPage onOpenConnector={(name) => void navigate({ to: "/admin/connectors/$name", params: { name } })} />
  );
}
