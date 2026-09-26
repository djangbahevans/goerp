import { createFileRoute } from "@tanstack/react-router";
import { HandoffPage } from "../../auth/handoff-page.js";
import { safeRedirect } from "../../auth/safe-redirect.js";

// shell-ux.md §2.11.
export const Route = createFileRoute("/auth/handoff")({
  validateSearch: (search: Record<string, unknown>): { code?: string; redirect?: string } => ({
    ...(typeof search.code === "string" ? { code: search.code } : {}),
    ...(typeof search.redirect === "string" ? { redirect: search.redirect } : {}),
  }),
  component: HandoffRoute,
});

function HandoffRoute() {
  const { code, redirect } = Route.useSearch();
  return <HandoffPage code={code} redirectTo={safeRedirect(redirect)} />;
}
