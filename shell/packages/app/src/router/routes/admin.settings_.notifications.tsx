import { createFileRoute } from "@tanstack/react-router";
import { AdminNotificationTemplatesPage } from "../../admin/notification-templates/admin-notification-templates-page.js";

// shell-ux.md §5.8. Not nested under /admin/settings, whose page has no outlet.
export const Route = createFileRoute("/admin/settings_/notifications")({
  staticData: { breadcrumb: "Notification templates" },
  component: AdminNotificationTemplatesPage,
});
