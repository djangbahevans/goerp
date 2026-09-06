import type { ReactNode } from "react";

// A page-local content sidebar for custom views (e.g. handed to
// TwoColumnLayout's `sidebar` slot) — distinct from the shell's own global
// navigation sidebar (shell-architecture.md §16), which is chrome module
// authors cannot modify.
export interface SidebarProps {
  width?: string | undefined;
  children: ReactNode;
}

export function Sidebar({ width, children }: SidebarProps): ReactNode {
  return (
    <aside style={width ? { width } : undefined} className="flex flex-col gap-4 border-border border-l p-4">
      {children}
    </aside>
  );
}
