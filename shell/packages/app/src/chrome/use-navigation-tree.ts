import { PermissionContext } from "@goerp/sdk/auth";
import { ViewRegistryContext } from "@goerp/sdk/schema";
import { useContext } from "react";
import { useConditionEvaluator } from "../conditions/use-condition-evaluator.js";
import type { NavigationGroup } from "./navigation-types.js";

// `tree` defaults to `viewRegistry.navigationTree` (built by
// buildViewRegistry/buildNavTree, packages/sdk/src/schema/view-registry.ts,
// from every loaded module's manifest `navigation` declarations) but takes
// an override so the permission-filtering logic below is testable against
// fixture data without a real ViewRegistryProvider. Outside a
// ViewRegistryProvider (e.g. a test that only wraps PermissionProvider),
// useContext returns null and this falls back to an empty tree rather than
// throwing — only the permission check below is a hard requirement.
export function useNavigationTree(tree?: NavigationGroup[]): NavigationGroup[] {
  const viewRegistry = useContext(ViewRegistryContext);
  const sourceTree = tree ?? viewRegistry?.navigationTree ?? [];

  const permissionContext = useContext(PermissionContext);
  const conditions = useConditionEvaluator("navigation");
  if (!permissionContext) {
    throw new Error("useNavigationTree must be used within a PermissionProvider");
  }
  const { check, moduleEnabled } = permissionContext;

  return sourceTree
    .filter(
      (group) =>
        moduleEnabled(group.module) &&
        (!group.permission || check(group.permission)) &&
        conditions.isVisible(group.condition, `group "${group.label}" condition`),
    )
    .map((group) => ({
      ...group,
      children: group.children.filter(
        (item) =>
          (!item.permission || check(item.permission)) &&
          conditions.isVisible(item.condition, `item "${item.label}" condition`),
      ),
    }))
    .filter((group) => group.children.length > 0);
}
