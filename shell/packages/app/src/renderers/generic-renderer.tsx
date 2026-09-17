import type { ResolvedView } from "@goerp/sdk/schema";
import { ViewDispatch } from "./view-dispatch.js";

// Renders a resolved full-page view — the /_m/$ catch-all route's own
// component. No PageLayout/PageHeader wrapping: each renderer owns its own
// full-page chrome (shell-architecture.md §20) whenever embedded is falsy.
// recordId, when resolved, flows through to ViewDispatch for edit vs.
// create mode — a form view reached with none (e.g. CreateActionButton's
// raw, unsubstituted {id} route) is already create mode via useFormRecord.
export function GenericRenderer({ resolvedView }: { resolvedView: ResolvedView }) {
  return (
    <ViewDispatch
      view={resolvedView.declaration}
      module={resolvedView.module}
      embedded={false}
      recordId={resolvedView.recordId}
    />
  );
}
