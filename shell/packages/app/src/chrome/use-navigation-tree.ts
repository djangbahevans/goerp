import { PermissionContext } from "@goerp/sdk/auth";
import { ViewRegistryContext } from "@goerp/sdk/schema";
import { useContext } from "react";
import { useConditionEvaluator } from "../conditions/use-condition-evaluator.js";
import { navigationStatus } from "./navigation-status.js";
import type { NavigationGroup } from "./navigation-types.js";

// `tree` overrides the registry's navigation tree; without either, the tree is empty.
export function useNavigationTree(tree?: NavigationGroup[]): NavigationGroup[] {
  const viewRegistry = useContext(ViewRegistryContext);
  const sourceTree = tree ?? viewRegistry?.navigationTree ?? [];

  const permissionContext = useContext(PermissionContext);
  const conditions = useConditionEvaluator("navigation");
  if (!permissionContext) {
    throw new Error("useNavigationTree must be used within a PermissionProvider");
  }
  const { check, moduleEnabled } = permissionContext;
  const shown = (gate: Parameters<typeof navigationStatus>[0], location: string) =>
    navigationStatus(gate, {
      moduleEnabled,
      check: (permission) => check(permission),
      conditionHolds: (condition) => conditions.isVisible(condition, location),
    }).shown;

  return sourceTree
    .filter((group) => shown(group, `group "${group.label}" condition`))
    .map((group) => ({
      ...group,
      children: group.children.filter((item) =>
        shown({ permission: item.permission, condition: item.condition }, `item "${item.label}" condition`),
      ),
    }))
    .filter((group) => group.children.length > 0);
}
