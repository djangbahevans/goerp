import { EmptyState, PageLayout } from "@goerp/sdk/components";
import { createFileRoute } from "@tanstack/react-router";
import { RouterTextLink } from "../text-link.js";

// First 403 page in the app — the /_m/$ catch-all route's beforeLoad
// (goerp#671) redirects here when a resolved view's module isn't enabled
// or its permissions aren't held; no other route guard exists yet to
// reuse this from.
export const Route = createFileRoute("/403")({
  component: ForbiddenPage,
});

function ForbiddenPage() {
  return (
    <PageLayout>
      <EmptyState
        icon="shield-alert"
        title="You don't have permission to view this page"
        description="If you think this is a mistake, contact your workspace administrator."
        action={
          <span className="text-sm">
            <RouterTextLink to="/">Go home</RouterTextLink>
          </span>
        }
      />
    </PageLayout>
  );
}
