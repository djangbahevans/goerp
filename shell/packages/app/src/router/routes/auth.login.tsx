import { createFileRoute } from "@tanstack/react-router";
import { isLoginNotice, type LoginNotice, LoginPage } from "../../auth/login-page.js";
import { safeRedirect } from "../../auth/safe-redirect.js";

// shell-ux.md §2.1.
export const Route = createFileRoute("/auth/login")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string; notice?: LoginNotice } => ({
    ...(typeof search.redirect === "string" ? { redirect: search.redirect } : {}),
    ...(isLoginNotice(search.notice) ? { notice: search.notice } : {}),
  }),
  component: LoginRoute,
});

// useSearch() merges in the raw URL params, so a value validateSearch
// dropped can still arrive here — both are re-checked before use.
function LoginRoute() {
  const { redirect, notice } = Route.useSearch();
  return <LoginPage redirectTo={safeRedirect(redirect)} notice={isLoginNotice(notice) ? notice : undefined} />;
}
