import { createFileRoute } from "@tanstack/react-router";
import { MFAChallengePage } from "../../auth/mfa-challenge-page.js";
import { safeRedirect } from "../../auth/safe-redirect.js";

// shell-ux.md §2.6.
export const Route = createFileRoute("/auth/mfa")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
  component: MFAChallengeRoute,
});

function MFAChallengeRoute() {
  const { redirect } = Route.useSearch();
  return <MFAChallengePage redirectTo={safeRedirect(redirect)} />;
}
