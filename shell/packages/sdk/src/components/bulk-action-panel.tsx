import type { ReactNode } from "react";

export interface BulkActionPanelProps {
  children: ReactNode;
}

// Rendered by a module's own bulk_actions "custom" component
// (view-system.md's "Bulk actions") after the shell invokes it — this is
// just the panel chrome; selection state and completion come from the
// `useBulkAction` hook the panel's own children call.
export function BulkActionPanel({ children }: BulkActionPanelProps): ReactNode {
  return (
    <section
      aria-label="Bulk action"
      className="flex items-center gap-3 rounded-structural border border-border bg-surface p-6 shadow-lg"
    >
      {children}
    </section>
  );
}
