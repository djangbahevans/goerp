import { createFileRoute } from "@tanstack/react-router";
import { TenantSuspendedPage } from "../../pages/errors/index.js";

// shell-ux.md §6.6. The root route sends every path here while a 403
// tenant_suspended is outstanding.
export const Route = createFileRoute("/tenant-suspended")({
  component: TenantSuspendedRoute,
});

function TenantSuspendedRoute() {
  return <TenantSuspendedPage supportUrl={import.meta.env.VITE_SUPPORT_URL || undefined} />;
}
