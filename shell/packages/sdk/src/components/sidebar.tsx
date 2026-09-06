import type { ReactNode } from "react";

// A fixed-width content sidebar for custom views (e.g. handed to
// TwoColumnLayout's `sidebar` slot, or rendering a manifest FormSidebar's
// sections) — distinct from the shell's own global navigation sidebar
// (shell-architecture.md §16), which is chrome module authors cannot modify.
export interface SidebarProps {
  // Matches FormSidebar.width's documented example and default (px).
  width?: number | undefined;
  children: ReactNode;
}

export function Sidebar({ width = 280, children }: SidebarProps): ReactNode {
  return (
    <aside style={{ width }} className="flex flex-col gap-4 border-border border-l p-4">
      {children}
    </aside>
  );
}
