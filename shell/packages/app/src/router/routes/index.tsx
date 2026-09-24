import { usePermissionsStatus } from "@goerp/sdk/auth";
import { ActionButton, EmptyState, PageLayout } from "@goerp/sdk/components";
import { useViewRegistryStatus } from "@goerp/sdk/schema";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";
import { useNavigationTree } from "../../chrome/use-navigation-tree.js";

// shell-architecture.md §6: "/ → redirect to first accessible module nav item".
export const Route = createFileRoute("/")({
  component: IndexPage,
});

function IndexPage() {
  // The nav tree is filtered by permissions, so both have to have loaded
  // before an empty tree means "nothing accessible" rather than "not yet" —
  // and a failed fetch leaves the same empty data behind, so it's reported
  // as a failure, not as missing access.
  const registryStatus = useViewRegistryStatus();
  const permissionsStatus = usePermissionsStatus();
  const failed = registryStatus === "error" || permissionsStatus === "error";
  const ready = registryStatus === "ready" && permissionsStatus === "ready";
  // External items open a new tab; `/` only lands on an in-app page.
  const firstPath = useNavigationTree()
    .flatMap((group) => group.children)
    .find((item) => !item.external)?.path;
  const navigate = useNavigate();

  useEffect(() => {
    if (ready && firstPath) void navigate({ href: firstPath, replace: true });
  }, [ready, firstPath, navigate]);

  if (failed) {
    return (
      <PageLayout>
        <EmptyState
          icon="circle-alert"
          title="Couldn't load your workspace"
          description="Check your connection and try again."
          action={<ActionButton onClick={() => window.location.reload()}>Reload</ActionButton>}
        />
      </PageLayout>
    );
  }

  if (!ready || firstPath) return null;

  return (
    <PageLayout>
      <EmptyState
        icon="layout-grid"
        title="No modules are available to you yet"
        description="Ask your workspace administrator for access."
      />
    </PageLayout>
  );
}
