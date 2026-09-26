import { createFileRoute } from "@tanstack/react-router";
import { SecurityPage } from "../../settings/security-page.js";

// shell-ux.md §4.3.
export const Route = createFileRoute("/settings/security")({
  component: () => <SecurityPage />,
});
