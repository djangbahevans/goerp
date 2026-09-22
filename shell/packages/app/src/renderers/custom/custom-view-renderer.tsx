import { ErrorBoundary, Skeleton } from "@goerp/sdk/components";
import { componentRegistry } from "@goerp/sdk/schema";
import { Suspense } from "react";
import type { CustomViewDeclaration } from "./custom-view-types.js";

export interface CustomViewRendererProps {
  view: CustomViewDeclaration;
  module: string;
  recordId?: string | undefined;
  embedded?: boolean | undefined;
  baseFilter?: Record<string, string> | undefined;
}

// Renders the registered component as the page body (view-system.md §9).
// - Forwards module/recordId/embedded/baseFilter like ListRendererProps,
//   so an embedded custom view (view-system.md §5) can scope itself.
// - Suspense+Skeleton covers a lazy chunk load; ErrorBoundary contains a
//   render error instead of blanking the page.
export function CustomViewRenderer({ view, module, recordId, embedded, baseFilter }: CustomViewRendererProps) {
  const Component = componentRegistry.tryResolve(view.component);
  if (!Component) {
    return (
      <p role="alert">
        "{view.component}" isn't a registered component — "{view.name}" can't be shown.
      </p>
    );
  }

  return (
    <ErrorBoundary
      fallback={(error) => (
        <p>
          "{view.name}" failed to render: {error.message}
        </p>
      )}
    >
      <Suspense fallback={<Skeleton />}>
        <Component
          module={module}
          {...(recordId !== undefined ? { recordId } : {})}
          {...(embedded !== undefined ? { embedded } : {})}
          {...(baseFilter !== undefined ? { baseFilter } : {})}
        />
      </Suspense>
    </ErrorBoundary>
  );
}
