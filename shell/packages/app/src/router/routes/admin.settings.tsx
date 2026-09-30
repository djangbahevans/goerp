import { createFileRoute } from "@tanstack/react-router";
import { TenantSettingsPage } from "../../admin/settings/tenant-settings-page.js";

// shell-ux.md §5.5 "Tenant settings".
export const Route = createFileRoute("/admin/settings")({
  staticData: { breadcrumb: "Settings" },
  component: TenantSettingsPage,
});
