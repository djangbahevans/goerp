import type { ReactNode } from "react";
import { useId, useState } from "react";

export interface SectionCardProps {
  title?: string | undefined;
  // Mirrors manifest-spec.md's FormSection.collapsible/collapsed_by_default,
  // so a hand-written section behaves like a manifest-declared one.
  collapsible?: boolean | undefined;
  defaultCollapsed?: boolean | undefined;
  children: ReactNode;
}

export function SectionCard({
  title,
  collapsible = false,
  defaultCollapsed = false,
  children,
}: SectionCardProps): ReactNode {
  const contentId = useId();
  const [collapsed, setCollapsed] = useState(collapsible && defaultCollapsed);

  return (
    <section className="rounded-lg border border-border bg-bg p-4">
      {(title !== undefined || collapsible) && (
        <div className="flex items-center justify-between">
          {title !== undefined && <h2 className="font-medium text-text">{title}</h2>}
          {collapsible && (
            <button
              type="button"
              onClick={() => setCollapsed((v) => !v)}
              aria-expanded={!collapsed}
              aria-controls={contentId}
            >
              {collapsed ? "Expand" : "Collapse"}
            </button>
          )}
        </div>
      )}
      {/* Always rendered — aria-controls must reference a real element even
          while collapsed, which `hidden` (not conditional unmounting)
          preserves. */}
      <div id={contentId} hidden={collapsed} className="mt-2">
        {children}
      </div>
    </section>
  );
}
