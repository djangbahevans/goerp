import type { CSSProperties, ReactNode } from "react";

// A fixed-width content sidebar for custom views (e.g. handed to
// TwoColumnLayout's `sidebar` slot, or rendering a manifest FormSidebar's
// sections) — distinct from the shell's own global navigation sidebar
// (shell-architecture.md §16), which is chrome module authors cannot modify.
export interface SidebarProps {
  // Matches FormSidebar.width's documented example and default (px).
  width?: number | undefined;
  children: ReactNode;
}

// Below 768px the sidebar stacks under the content at full width (sidebar.md);
// from 768px up it takes its own width, through a CSS variable so the width
// has no effect below that.
export function Sidebar({ width = 280, children }: SidebarProps): ReactNode {
  return (
    <aside
      style={{ "--sidebar-width": `${width}px` } as CSSProperties}
      className="flex flex-col gap-4 border-border border-t p-4 md:w-(--sidebar-width) md:shrink-0 md:border-t-0 md:border-l"
    >
      {children}
    </aside>
  );
}
