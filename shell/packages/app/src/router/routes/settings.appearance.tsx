import { createFileRoute } from "@tanstack/react-router";
import { AppearancePage } from "../../settings/appearance-page.js";

// shell-ux.md §4.4.
export const Route = createFileRoute("/settings/appearance")({
  component: () => <AppearancePage />,
});
