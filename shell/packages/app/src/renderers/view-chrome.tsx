import { PageHeader, PageLayout } from "@goerp/sdk/components";
import type { ReactNode } from "react";

// list-renderer.md's page composition, shared by the list, kanban,
// calendar, pivot and timeline renderers.

// shell-architecture.md §20: a full-page render owns its page chrome; an
// embedded one leaves it to the parent form.
export function ViewPage({
  embedded,
  title,
  actions,
  children,
}: {
  embedded: boolean | undefined;
  title: string;
  actions?: ReactNode;
  children: ReactNode;
}): ReactNode {
  if (embedded) return children;
  return (
    <PageLayout>
      <PageHeader title={title} {...(actions !== undefined ? { actions } : {})} />
      {children}
    </PageLayout>
  );
}

export function ViewSurface({ children }: { children: ReactNode }): ReactNode {
  return <div className="rounded-structural border border-border bg-surface">{children}</div>;
}

// Hidden when neither region renders a control: ListFilters and the
// controls can each come back empty after condition/permission checks.
const HIDE_WHEN_NO_CONTROLS = "[&:not(:has(button,input,a))]:hidden";

export function ViewToolbar({
  filters,
  controls,
  standalone = false,
}: {
  filters: ReactNode;
  controls?: ReactNode;
  // Kanban: a bar of its own above the board, not the top of a surface.
  standalone?: boolean;
}): ReactNode {
  const frame = standalone ? "rounded-structural border border-border bg-surface" : "border-border border-b";
  return (
    <div className={`flex flex-wrap items-end justify-between gap-4 p-3 ${frame} ${HIDE_WHEN_NO_CONTROLS}`}>
      {filters}
      {controls !== undefined && <div className="ms-auto flex flex-wrap items-end justify-end gap-2">{controls}</div>}
    </div>
  );
}
