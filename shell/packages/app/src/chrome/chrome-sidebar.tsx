import type { ReactNode } from "react";
import { NavGroupSection } from "./nav-group.js";
import type { NavigationGroup } from "./navigation-types.js";
import type { SidebarStoreLike } from "./sidebar-store.js";
import { useSidebar } from "./sidebar-store.js";
import { useNavigationTree } from "./use-navigation-tree.js";

// shell-architecture.md §15/§16: the shell's persistent left-edge
// navigation rail, mounted once by ChromeLayout (unbuilt, a later ticket,
// same posture as chrome-header.tsx). Its own tree data source
// (viewRegistry.navigationTree) is also unbuilt — see use-navigation-tree.ts
// — so this renders an empty-but-correct <nav> until that lands.
//
// `tree`/`store` exist only for Storybook/tests to substitute fixture data
// and a deterministic sidebar-state store in place of the real (currently
// empty) tree source and the real localStorage-backed singleton — the same
// injectable-default shape as useTheme(store)/useSidebar(store). ChromeLayout,
// the only production caller, never passes either — matching the doc's own
// "API/Props: None" contract for every real usage.
export function ChromeSidebar({
  tree: treeOverride,
  store,
}: {
  tree?: NavigationGroup[];
  store?: SidebarStoreLike;
} = {}): ReactNode {
  const { collapsed, expandedGroups, toggleGroup } = useSidebar(store);
  const tree = useNavigationTree(treeOverride);

  return (
    <nav
      aria-label="Main"
      style={{ borderInlineEnd: "1px solid var(--color-border)" }}
      className={`flex h-full flex-col overflow-y-auto bg-surface transition-[width] duration-(--duration-base) ease-out motion-reduce:transition-none ${
        collapsed ? "w-(--sidebar-collapsed-width)" : "w-(--sidebar-width)"
      }`}
    >
      {tree.map((group) => (
        <NavGroupSection
          key={group.key}
          group={group}
          collapsed={collapsed}
          expanded={expandedGroups.has(group.key)}
          onToggle={toggleGroup}
        />
      ))}
    </nav>
  );
}
