import { createFileRoute } from "@tanstack/react-router";
import { ForbiddenPage, type ForbiddenReason, isForbiddenReason } from "../../pages/errors/index.js";

// shell-ux.md §6.2. The /_m/$ catch-all's beforeLoad redirects here with the
// reason, and the module for module_not_enabled.
export const Route = createFileRoute("/403")({
  validateSearch: (search: Record<string, unknown>): { reason?: ForbiddenReason; module?: string } => ({
    ...(isForbiddenReason(search.reason) ? { reason: search.reason } : {}),
    ...(typeof search.module === "string" ? { module: search.module } : {}),
  }),
  component: ForbiddenRoute,
});

// useSearch() merges in the raw URL params, so a value validateSearch
// dropped can still arrive here — both are re-checked before use.
function ForbiddenRoute() {
  const { reason, module } = Route.useSearch();
  return (
    <ForbiddenPage
      reason={isForbiddenReason(reason) ? reason : "missing_permission"}
      module={typeof module === "string" ? module : undefined}
    />
  );
}
