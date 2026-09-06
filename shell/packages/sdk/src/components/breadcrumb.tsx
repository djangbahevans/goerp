import type { ReactNode } from "react";

export interface BreadcrumbItem {
  label: string;
  onClick?: (() => void) | undefined;
}

// An in-view, hierarchical drill-down trail with nothing to do with routing
// (e.g. a document library's folder drill-down) — distinct from the shell's
// route-derived global header breadcrumb (shell-architecture.md §17), which
// PageHeader already leaves to the shell. `onClick`, not `href`: each level
// is local component state, not a route, so there's nothing to link to.
export interface BreadcrumbProps {
  items: BreadcrumbItem[];
}

export function Breadcrumb({ items }: BreadcrumbProps): ReactNode {
  return (
    <nav aria-label="Breadcrumb">
      <ol className="flex items-center gap-1 text-fg text-sm">
        {items.map((item, i) => {
          const isLast = i === items.length - 1;
          return (
            <li key={item.label} className="flex items-center gap-1">
              {i > 0 && <span aria-hidden="true">/</span>}
              {item.onClick !== undefined ? (
                <button type="button" onClick={item.onClick} aria-current={isLast ? "page" : undefined}>
                  {item.label}
                </button>
              ) : (
                <span aria-current={isLast ? "page" : undefined}>{item.label}</span>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
