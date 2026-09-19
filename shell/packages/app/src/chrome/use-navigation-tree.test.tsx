import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetReportedConditionErrors } from "../conditions/use-condition-evaluator.js";
import type { NavigationGroup } from "./navigation-types.js";
import { useNavigationTree } from "./use-navigation-tree.js";

function wrapper(permissions: string[] = [], modulesEnabled: string[] = []) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(modulesEnabled),
  });
  return ({ children }: { children: ReactNode }) => (
    <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>
  );
}

const FIXTURE_TREE: NavigationGroup[] = [
  {
    key: "sales",
    label: "Sales",
    icon: "home",
    module: "sales",
    children: [
      { key: "orders", label: "Orders", path: "/sales/orders", icon: "home" },
      { key: "reports", label: "Reports", path: "/sales/reports", icon: "home", permission: "sales.reports.view" },
    ],
  },
  {
    key: "hr",
    label: "HR",
    icon: "users",
    module: "hr",
    permission: "hr.view",
    children: [{ key: "employees", label: "Employees", path: "/hr/employees", icon: "users" }],
  },
];

describe("useNavigationTree", () => {
  it("throws outside a PermissionProvider", () => {
    expect(() => renderHook(() => useNavigationTree(FIXTURE_TREE))).toThrow(
      "useNavigationTree must be used within a PermissionProvider",
    );
  });

  it("drops an entire group whose module isn't enabled", () => {
    const { result } = renderHook(() => useNavigationTree(FIXTURE_TREE), { wrapper: wrapper([], []) });
    expect(result.current).toEqual([]);
  });

  it("drops a group gated by a permission the caller lacks, even with its module enabled", () => {
    const { result } = renderHook(() => useNavigationTree(FIXTURE_TREE), {
      wrapper: wrapper([], ["sales", "hr"]),
    });
    expect(result.current.map((g) => g.key)).toEqual(["sales"]);
  });

  it("drops a permission-gated child item but keeps the rest of its group", () => {
    const { result } = renderHook(() => useNavigationTree(FIXTURE_TREE), {
      wrapper: wrapper([], ["sales"]),
    });
    expect(result.current).toHaveLength(1);
    expect(result.current[0]?.children.map((c) => c.key)).toEqual(["orders"]);
  });

  it("keeps groups and items the caller has permission for", () => {
    const { result } = renderHook(() => useNavigationTree(FIXTURE_TREE), {
      wrapper: wrapper(["sales.reports.view", "hr.view"], ["sales", "hr"]),
    });
    expect(result.current.map((g) => g.key)).toEqual(["sales", "hr"]);
    expect(result.current[0]?.children.map((c) => c.key)).toEqual(["orders", "reports"]);
  });
});

describe("useNavigationTree conditions", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetReportedConditionErrors();
  });

  function tree(itemCondition?: string, groupCondition?: string): NavigationGroup[] {
    return [
      {
        key: "sales",
        label: "Sales",
        icon: "home",
        module: "sales",
        ...(groupCondition !== undefined ? { condition: groupCondition } : {}),
        children: [
          { key: "orders", label: "Orders", path: "/sales/orders", icon: "home" },
          {
            key: "admin",
            label: "Admin",
            path: "/sales/admin",
            icon: "home",
            ...(itemCondition !== undefined ? { condition: itemCondition } : {}),
          },
        ],
      },
    ];
  }

  it("keeps an item whose condition holds", () => {
    const { result } = renderHook(() => useNavigationTree(tree("user_has_permission('sales.admin')")), {
      wrapper: wrapper(["sales.admin"], ["sales"]),
    });
    expect(result.current[0]?.children.map((c) => c.key)).toEqual(["orders", "admin"]);
  });

  it("drops an item whose condition is false", () => {
    const { result } = renderHook(() => useNavigationTree(tree("user_has_permission('sales.admin')")), {
      wrapper: wrapper([], ["sales"]),
    });
    expect(result.current[0]?.children.map((c) => c.key)).toEqual(["orders"]);
  });

  it("drops a group whose condition is false", () => {
    const { result } = renderHook(() => useNavigationTree(tree(undefined, "user_has_permission('sales.admin')")), {
      wrapper: wrapper([], ["sales"]),
    });
    expect(result.current).toEqual([]);
  });

  it("drops an item, and reports it, when its condition is malformed", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    const { result } = renderHook(() => useNavigationTree(tree("user_has_permission(")), {
      wrapper: wrapper([], ["sales"]),
    });
    expect(result.current[0]?.children.map((c) => c.key)).toEqual(["orders"]);
    expect(String(consoleError.mock.calls[0]?.[0])).toContain('item "Admin" condition');
  });
});
