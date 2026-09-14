import type { ResolvedView } from "@goerp/sdk/schema";
import { ViewDispatch } from "./view-dispatch.js";

// Renders a resolved full-page view — the /_m/$ catch-all route's own
// component (router/routes/_m.$.tsx). No PageLayout/PageHeader wrapping
// here: per shell-architecture.md §20, each renderer already owns its own
// full-page chrome internally whenever embedded is falsy.
export function GenericRenderer({ resolvedView }: { resolvedView: ResolvedView }) {
  return <ViewDispatch view={resolvedView.declaration} module={resolvedView.module} embedded={false} />;
}
