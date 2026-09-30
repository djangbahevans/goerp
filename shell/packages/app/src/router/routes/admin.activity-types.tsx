import { createFileRoute } from "@tanstack/react-router";
import { AdminActivityTypesPage } from "../../admin/activity-types/admin-activity-types-page.js";

// shell-ux.md §5.10.
export const Route = createFileRoute("/admin/activity-types")({
  staticData: { breadcrumb: "Activity types" },
  component: AdminActivityTypesPage,
});
