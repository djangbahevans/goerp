import { PermissionContext } from "@goerp/sdk/auth";
import { useContext } from "react";
import type { NavigationGroup } from "./navigation-types.js";

// shell-architecture.md §9/§16: the real tree is `viewRegistry.navigationTree`,
// built by `buildViewRegistry` (§9 "Building the registry") from every
// loaded module's manifest `navigation` declarations. That registry doesn't
// exist in code yet — tracked as goerp#671 ("View registry: dynamic module
// routing, nav tree, capability filtering"), open and unblocked. Until it
// lands, no module contributes to this tree, so it starts empty rather than
// guessing at placeholder product content. ChromeSidebar renders correctly
// either way (an empty `<nav>`) and needs no changes once real data lands
// here.
const SOURCE_TREE: NavigationGroup[] = [];

// `tree` defaults to the (currently empty) real source but takes an
// override so the permission-filtering logic below is testable against
// fixture data without waiting on the real registry.
export function useNavigationTree(tree: NavigationGroup[] = SOURCE_TREE): NavigationGroup[] {
  const permissionContext = useContext(PermissionContext);
  if (!permissionContext) {
    throw new Error("useNavigationTree must be used within a PermissionProvider");
  }
  const { check, moduleEnabled } = permissionContext;

  return tree
    .filter((group) => moduleEnabled(group.module) && (!group.permission || check(group.permission)))
    .map((group) => ({
      ...group,
      children: group.children.filter((item) => !item.permission || check(item.permission)),
    }))
    .filter((group) => group.children.length > 0);
}
