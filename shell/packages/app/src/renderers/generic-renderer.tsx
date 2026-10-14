import type { ResolvedView } from "@goerp/sdk/schema";
import { ViewDispatch } from "./view-dispatch.js";

export function GenericRenderer({ resolvedView, params }: { resolvedView: ResolvedView; params?: URLSearchParams }) {
  return (
    <ViewDispatch
      view={resolvedView.declaration}
      module={resolvedView.module}
      embedded={false}
      recordId={resolvedView.recordId}
      {...(params ? { params } : {})}
    />
  );
}
