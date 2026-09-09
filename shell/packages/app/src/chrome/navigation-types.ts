import type { LucideIcon } from "lucide-react";

// shell-architecture.md §9/§16: the shape a module manifest's `navigation`
// array declares, and what `viewRegistry.navigationTree` (unbuilt, goerp#575
// /#674 — see use-navigation-tree.ts) will eventually merge across modules.
export interface NavigationItem {
  key: string;
  label: string;
  path: string;
  icon: LucideIcon;
  permission?: string;
  badgeCountRoute?: string;
}

export interface NavigationGroup {
  key: string;
  label: string;
  icon: LucideIcon;
  module: string;
  permission?: string;
  children: NavigationItem[];
}
