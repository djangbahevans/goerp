import { createFileRoute } from "@tanstack/react-router";
import { NotificationsPage } from "../../settings/notifications-page.js";

// shell-ux.md §4.2.
export const Route = createFileRoute("/settings/notifications")({
  component: () => <NotificationsPage />,
});
