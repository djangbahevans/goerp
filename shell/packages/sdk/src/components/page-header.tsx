import type { ReactNode } from "react";

export interface PageHeaderProps {
  title: string;
  subtitle?: string | undefined;
  actions?: ReactNode | undefined;
  // No `breadcrumbs` prop: the breadcrumb trail is owned by the shell's
  // global header (shell-architecture.md §17), derived from the route for
  // every view type — not a per-view concern.
}

export function PageHeader({ title, subtitle, actions }: PageHeaderProps): ReactNode {
  return (
    <header className="flex items-start justify-between gap-4">
      <div>
        <h1 className="font-semibold text-text text-xl">{title}</h1>
        {subtitle !== undefined && <p className="text-text text-sm">{subtitle}</p>}
      </div>
      {actions !== undefined && <div className="flex items-center gap-2">{actions}</div>}
    </header>
  );
}
