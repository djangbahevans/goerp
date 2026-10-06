import { createFileRoute } from "@tanstack/react-router";
import { ConnectorDetailPage } from "../../admin/connectors/connector-detail-page.js";

// shell-ux.md §5.4 "Connector detail".
export const Route = createFileRoute("/admin/connectors/$name")({
  staticData: { breadcrumb: "Connector" },
  component: ConnectorRoute,
});

function ConnectorRoute() {
  const { name } = Route.useParams();
  const navigate = Route.useNavigate();
  return <ConnectorDetailPage key={name} name={name} onBackToList={() => void navigate({ to: "/admin/connectors" })} />;
}
