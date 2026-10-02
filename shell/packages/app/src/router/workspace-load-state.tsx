import { ActionButton, EmptyState, PageLayout, Spinner } from "@goerp/sdk/components";

export function WorkspaceLoadError() {
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

export function WorkspaceLoading() {
  return (
    <PageLayout>
      <div role="status" aria-busy="true" className="flex items-center gap-3 p-6">
        <Spinner size={24} />
        <span>Loading your workspace…</span>
      </div>
    </PageLayout>
  );
}
