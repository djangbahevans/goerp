import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { renderHook } from "@testing-library/react";
import { Home, Users } from "lucide-react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
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
    icon: Home,
    module: "sales",
    children: [
      { key: "orders", label: "Orders", path: "/sales/orders", icon: Home },
      { key: "reports", label: "Reports", path: "/sales/reports", icon: Home, permission: "sales.reports.view" },
    ],
  },
  {
    key: "hr",
    label: "HR",
    icon: Users,
    module: "hr",
    permission: "hr.view",
    children: [{ key: "employees", label: "Employees", path: "/hr/employees", icon: Users }],
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
