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
    <section className="rounded-structural border border-border bg-surface p-4">
      {(title !== undefined || collapsible) && (
        <div className="flex items-center justify-between">
          {title !== undefined && <h2 className="font-medium text-text">{title}</h2>}
          {collapsible && (
            <button
              type="button"
              onClick={() => setCollapsed((v) => !v)}
              aria-expanded={!collapsed}
              aria-controls={contentId}
              className="rounded-control p-1 text-text-secondary transition-colors duration-(--duration-fast) ease-out hover:text-text focus-visible:outline-none focus-visible:shadow-focus motion-reduce:transition-none"
            >
              {collapsed ? "Expand" : "Collapse"}
            </button>
          )}
        </div>
      )}
      {/* Always rendered — aria-controls must reference a real element even
          while collapsed, which `hidden` (not conditional unmounting)
          preserves. The grid-rows/display transition (with @starting-style
          via `starting:`) animates in/out of `hidden`'s display:none instead
          of snapping, while staying genuinely hidden — not just visually
          collapsed — at rest. */}
      <div
        id={contentId}
        hidden={collapsed}
        className="mt-3 grid grid-rows-[1fr] transition-[grid-template-rows,display] transition-discrete duration-(--duration-base) ease-out starting:grid-rows-[0fr] [[hidden]]:grid-rows-[0fr] [[hidden]]:ease-in motion-reduce:transition-none"
      >
        {/* -m-1 p-1 (canceling out, so children land at the same visual
            position) pushes the actual clip boundary 4px past the content
            box on every side — exactly --shadow-focus's own spread — so a
            focused control flush against this section's own edge (a
            rightmost-column field, the last row) keeps its full focus ring
            instead of this wrapper (needed only to hide mid-transition
            overflow while collapsing/expanding) clipping it at rest. */}
        <div className="-m-1 overflow-hidden p-1">{children}</div>
      </div>
    </section>
  );
}
