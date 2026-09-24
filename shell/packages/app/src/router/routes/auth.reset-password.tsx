import { createFileRoute } from "@tanstack/react-router";
import { ResetPasswordPage } from "../../auth/reset-password-page.js";

// shell-ux.md §2.4: the emailed link is /auth/reset-password?token=…&tenant=….
export const Route = createFileRoute("/auth/reset-password")({
  validateSearch: (search: Record<string, unknown>): { token?: string; tenant?: string } => ({
    ...(typeof search.token === "string" ? { token: search.token } : {}),
    ...(typeof search.tenant === "string" ? { tenant: search.tenant } : {}),
  }),
  component: ResetPasswordRoute,
});

function ResetPasswordRoute() {
  const { token, tenant } = Route.useSearch();
  return <ResetPasswordPage token={token || undefined} tenant={tenant || undefined} />;
}
