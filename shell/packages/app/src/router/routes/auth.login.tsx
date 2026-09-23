import { createFileRoute } from "@tanstack/react-router";
import { LoginPage } from "../../auth/login-page.js";
import { safeRedirect } from "../../auth/safe-redirect.js";

// shell-ux.md §2.1.
export const Route = createFileRoute("/auth/login")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
  component: LoginRoute,
});

function LoginRoute() {
  const { redirect } = Route.useSearch();
  return <LoginPage redirectTo={safeRedirect(redirect)} />;
}
