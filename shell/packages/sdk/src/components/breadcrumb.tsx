import type { ReactNode } from "react";

export interface BreadcrumbItem {
  label: string;
  href?: string | undefined;
}

// A manual breadcrumb trail for content a custom view renders inside itself
// — distinct from the shell's global header breadcrumb (shell-architecture.md
// §17), which PageHeader already leaves to the shell, derived from the route.
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
              {item.href !== undefined && !isLast ? (
                <a href={item.href}>{item.label}</a>
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
