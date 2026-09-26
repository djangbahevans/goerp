import { createFileRoute } from "@tanstack/react-router";
import { safeRedirect } from "../../auth/safe-redirect.js";
import { SelectTenantPage } from "../../auth/select-tenant-page.js";

// shell-ux.md §2.9.
export const Route = createFileRoute("/auth/select-tenant")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
  component: SelectTenantRoute,
});

function SelectTenantRoute() {
  const { redirect } = Route.useSearch();
  return <SelectTenantPage redirectTo={safeRedirect(redirect)} />;
}
