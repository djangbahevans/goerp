export interface NavigationGate {
  // Set on a group; its items inherit the group's entitlement.
  module?: string | undefined;
  permission?: string | undefined;
  condition?: string | undefined;
}

export interface NavigationChecks {
  moduleEnabled: (module: string) => boolean;
  check: (permission: string) => boolean;
  conditionHolds: (condition: string) => boolean;
}

export interface NavigationStatus {
  shown: boolean;
  reasons: string[];
}

// Each reason a navigation group or item is filtered out of the sidebar.
export function navigationStatus(gate: NavigationGate, checks: NavigationChecks): NavigationStatus {
  const reasons: string[] = [];
  if (gate.module !== undefined && !checks.moduleEnabled(gate.module)) {
    reasons.push(`module "${gate.module}" is not entitled`);
  }
  if (gate.permission && !checks.check(gate.permission)) reasons.push(`missing permission "${gate.permission}"`);
  if (gate.condition && !checks.conditionHolds(gate.condition)) reasons.push(`condition is false: ${gate.condition}`);
  return { shown: reasons.length === 0, reasons };
}
