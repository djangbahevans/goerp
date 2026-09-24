import { createFileRoute } from "@tanstack/react-router";
import { VerifyEmailPage } from "../../auth/verify-email-page.js";

// shell-ux.md §2.10: the emailed link is /auth/verify-email?token=…&tenant=….
export const Route = createFileRoute("/auth/verify-email")({
  validateSearch: (search: Record<string, unknown>): { token?: string; tenant?: string } => ({
    ...(typeof search.token === "string" ? { token: search.token } : {}),
    ...(typeof search.tenant === "string" ? { tenant: search.tenant } : {}),
  }),
  component: VerifyEmailRoute,
});

function VerifyEmailRoute() {
  const { token, tenant } = Route.useSearch();
  return <VerifyEmailPage token={token || undefined} tenant={tenant || undefined} />;
}
