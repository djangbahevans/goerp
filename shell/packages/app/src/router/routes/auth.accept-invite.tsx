import { createFileRoute } from "@tanstack/react-router";
import { AcceptInvitePage } from "../../auth/accept-invite-page.js";

// shell-ux.md §2.5: the emailed link is /auth/accept-invite?token=…&tenant=….
export const Route = createFileRoute("/auth/accept-invite")({
  validateSearch: (search: Record<string, unknown>): { token?: string; tenant?: string } => ({
    ...(typeof search.token === "string" ? { token: search.token } : {}),
    ...(typeof search.tenant === "string" ? { tenant: search.tenant } : {}),
  }),
  component: AcceptInviteRoute,
});

function AcceptInviteRoute() {
  const { token, tenant } = Route.useSearch();
  return <AcceptInvitePage token={token || undefined} tenant={tenant || undefined} />;
}
