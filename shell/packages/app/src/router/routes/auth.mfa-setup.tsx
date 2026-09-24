import { createFileRoute, redirect } from "@tanstack/react-router";
import { MFASetupPage } from "../../auth/mfa-setup-page.js";
import { safeRedirect } from "../../auth/safe-redirect.js";

// shell-ux.md §2.7. The root route sends a user whose mfaSetupRequired is set
// here; this route sends everyone else away.
export const Route = createFileRoute("/auth/mfa-setup")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
  beforeLoad: ({ context, search }) => {
    const { status } = context.auth.state;
    if (status === "idle" || status === "checking") return;
    if (!context.auth.isAuthenticated) {
      throw redirect({ to: "/auth/login", search: search.redirect ? { redirect: search.redirect } : {} });
    }
    if (!context.auth.user?.mfaSetupRequired) {
      throw redirect({ href: safeRedirect(search.redirect), replace: true });
    }
  },
  component: MFASetupRoute,
});

function MFASetupRoute() {
  const { redirect: redirectParam } = Route.useSearch();
  return <MFASetupPage redirectTo={safeRedirect(redirectParam)} />;
}
