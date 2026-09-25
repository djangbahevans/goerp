import type { AuthContextValue, CurrentTenant, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetReportedConditionErrors, useConditionEvaluator } from "./use-condition-evaluator.js";

const user: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  contactId: "c1",
  name: null,
  avatarUrl: null,
  roles: ["sales_manager"],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant: CurrentTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};

function session(overrides: Partial<CurrentUser> = {}, permissions: string[] = ["sales:order:confirm"]) {
  const auth = {
    state: { status: "authenticated", user: { ...user, ...overrides }, tenant },
    isAuthenticated: true,
    user: { ...user, ...overrides },
    tenant,
  } as unknown as AuthContextValue;
  const perms = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return ({ children }: { children: ReactNode }) => (
    <AuthContext.Provider value={auth}>
      <PermissionContext.Provider value={perms}>{children}</PermissionContext.Provider>
    </AuthContext.Provider>
  );
}

let consoleError: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  resetReportedConditionErrors();
  consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  consoleError.mockRestore();
});

function evaluator(wrapper = session()) {
  return renderHook(() => useConditionEvaluator('form "orders"'), { wrapper }).result.current;
}

describe("useConditionEvaluator", () => {
  it("shows an element with no condition and leaves it editable", () => {
    const conditions = evaluator();
    expect(conditions.isVisible(undefined, "field a")).toBe(true);
    expect(conditions.isReadonly(undefined, "field a")).toBe(false);
  });

  it("evaluates a condition against the record", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("record.state = 'draft'", "field a", { state: "draft" })).toBe(true);
    expect(conditions.isVisible("record.state = 'draft'", "field a", { state: "done" })).toBe(false);
    expect(conditions.isReadonly("record.state != 'draft'", "field a", { state: "done" })).toBe(true);
    expect(conditions.isReadonly("record.state != 'draft'", "field a", { state: "draft" })).toBe(false);
  });

  it("treats a reference to an absent record field as unknown, so the condition is false", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("record.state = 'draft'", "field a")).toBe(false);
    expect(consoleError).not.toHaveBeenCalled();
  });

  it("resolves user.id, user.contact_id and user.tenant_id from the session", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("user.id = 'u1'", "x")).toBe(true);
    expect(conditions.isVisible("user.contact_id = 'c1'", "x")).toBe(true);
    expect(conditions.isVisible("user.tenant_id = 't1'", "x")).toBe(true);
    expect(conditions.isVisible("record.owner_id = user.contact_id", "x", { owner_id: "c1" })).toBe(true);
    expect(conditions.isVisible("record.owner_id = user.contact_id", "x", { owner_id: "c2" })).toBe(false);
  });

  it("resolves user.contact_id to null for a user with no linked contact", () => {
    const conditions = evaluator(session({ contactId: null }));
    expect(conditions.isVisible("user.contact_id IS NULL", "x")).toBe(true);
  });

  it("resolves user_has_role from the session's roles", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("user_has_role('sales_manager')", "x")).toBe(true);
    expect(conditions.isVisible("user_has_role('admin')", "x")).toBe(false);
  });

  it("resolves user_has_permission from the permission context", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("user_has_permission('sales:order:confirm')", "x")).toBe(true);
    expect(conditions.isVisible("user_has_permission('sales:order:delete')", "x")).toBe(false);
  });

  it("denies role and permission conditions with no providers, without reporting an error", () => {
    const conditions = evaluator(({ children }: { children: ReactNode }) => <>{children}</>);
    expect(conditions.isVisible("user_has_role('sales_manager')", "x")).toBe(false);
    expect(conditions.isVisible("user_has_permission('sales:order:confirm')", "x")).toBe(false);
    expect(conditions.isVisible("record.state = 'draft'", "x", { state: "draft" })).toBe(true);
    expect(consoleError).not.toHaveBeenCalled();
  });

  it("reads permissions from the permission context even with no session", () => {
    const perms = createPermissionContextValue({
      permissions: new Set(["sales:order:confirm"]),
      fieldAccess: {},
      modulesEnabled: new Set(),
    });
    const conditions = evaluator(({ children }: { children: ReactNode }) => (
      <PermissionContext.Provider value={perms}>{children}</PermissionContext.Provider>
    ));
    expect(conditions.isVisible("user_has_permission('sales:order:confirm')", "x")).toBe(true);
    expect(conditions.isVisible("user_has_role('sales_manager')", "x")).toBe(false);
  });

  it("hides and locks on a parse error, distinct from a false condition, and reports it", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("record.state ==", "field state")).toBe(false);
    expect(conditions.isReadonly("record.state ==", "field state")).toBe(true);
    expect(consoleError).toHaveBeenCalledTimes(1);
    const message = String(consoleError.mock.calls[0]?.[0]);
    expect(message).toContain('form "orders"');
    expect(message).toContain("field state");
    expect(message).toContain("record.state ==");
  });

  it("hides and locks on an expression that evaluates to a non-boolean", () => {
    const conditions = evaluator();
    expect(conditions.isVisible("record.amount", "field amount", { amount: 5 })).toBe(false);
    expect(conditions.isReadonly("record.amount", "field amount", { amount: 5 })).toBe(true);
    expect(consoleError).toHaveBeenCalledTimes(1);
  });

  it("does not throw on an unsupported operator such as LIKE", () => {
    const conditions = evaluator();
    expect(() => conditions.isVisible("record.name LIKE 'a%'", "field name", { name: "abc" })).not.toThrow();
    expect(conditions.isVisible("record.name LIKE 'a%'", "field name", { name: "abc" })).toBe(false);
  });

  it("reports the same broken expression at the same location once across re-evaluations", () => {
    const conditions = evaluator();
    for (let i = 0; i < 5; i++) conditions.isVisible("record.state ==", "field state");
    expect(consoleError).toHaveBeenCalledTimes(1);
    conditions.isVisible("record.state ==", "section main");
    expect(consoleError).toHaveBeenCalledTimes(2);
  });

  it("picks up a session change on re-render", () => {
    let roles = ["sales_manager"];
    const { result, rerender } = renderHook(() => useConditionEvaluator("form"), {
      wrapper: ({ children }: { children: ReactNode }) => {
        const Wrapper = session({ roles });
        return <Wrapper>{children}</Wrapper>;
      },
    });
    expect(result.current.isVisible("user_has_role('admin')", "x")).toBe(false);
    roles = ["admin"];
    rerender();
    expect(result.current.isVisible("user_has_role('admin')", "x")).toBe(true);
  });

  it("computes a value expression against the record", () => {
    const conditions = evaluator();
    expect(conditions.computeValue("record.a + record.b", "field x", { a: 1, b: 2 })).toEqual({ ok: true, value: 3 });
    expect(conditions.computeValue("PERCENT(record.a, 1)", "field x", { a: 0.123 })).toEqual({
      ok: true,
      value: "12.3%",
    });
  });

  it("fails without a console report for a data-dependent evaluation failure", () => {
    const conditions = evaluator();
    expect(conditions.computeValue("record.a / record.b", "field x", { a: 1, b: 0 })).toMatchObject({ ok: false });
    expect(conditions.computeValue("record.a", "field x", {})).toMatchObject({ ok: false });
    expect(consoleError).not.toHaveBeenCalled();
  });

  it("fails, and reports once, for a malformed value expression", () => {
    const conditions = evaluator();
    for (let i = 0; i < 3; i++) {
      expect(conditions.computeValue("record.a +", "field x", { a: 1 })).toMatchObject({ ok: false });
    }
    expect(consoleError).toHaveBeenCalledTimes(1);
    expect(String(consoleError.mock.calls[0]?.[0])).toContain("(expression: record.a +)");
  });
});
