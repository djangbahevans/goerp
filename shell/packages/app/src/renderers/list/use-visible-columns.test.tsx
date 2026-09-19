import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetReportedConditionErrors } from "../../conditions/use-condition-evaluator.js";
import type { ListColumn } from "./list-view-types.js";
import { filterColumnsByFieldAccess, useVisibleColumns } from "./use-visible-columns.js";

describe("filterColumnsByFieldAccess", () => {
  it("drops a column the caller lacks read access to", () => {
    const columns: ListColumn[] = [{ field: "name" }, { field: "ssn" }];
    const checkField = (_model: string, field: string) => field !== "ssn";

    expect(filterColumnsByFieldAccess(columns, "contacts.contact", checkField)).toEqual([{ field: "name" }]);
  });

  it("checks read access against the view's own resource", () => {
    const columns: ListColumn[] = [{ field: "name" }];
    const seen: string[] = [];
    const checkField = (model: string, field: string) => {
      seen.push(`${model}.${field}`);
      return true;
    };

    filterColumnsByFieldAccess(columns, "contacts.contact", checkField);

    expect(seen).toEqual(["contacts.contact.name"]);
  });

  it("drops a column marked hidden even when the caller has read access", () => {
    const columns: ListColumn[] = [{ field: "name" }, { field: "internal_note", hidden: true }];
    expect(filterColumnsByFieldAccess(columns, "contacts.contact", () => true)).toEqual([{ field: "name" }]);
  });

  it("includes a hidden column once its field is in revealedFields", () => {
    const columns: ListColumn[] = [{ field: "name" }, { field: "internal_note", hidden: true }];
    const revealed = new Set(["internal_note"]);
    expect(filterColumnsByFieldAccess(columns, "contacts.contact", () => true, revealed)).toEqual(columns);
  });

  it("still drops a revealed hidden column the caller lacks read access to", () => {
    const columns: ListColumn[] = [{ field: "internal_note", hidden: true }];
    const revealed = new Set(["internal_note"]);
    expect(filterColumnsByFieldAccess(columns, "contacts.contact", () => false, revealed)).toEqual([]);
  });
});

function wrapperWithFieldAccess(fieldAccess: Record<string, Record<string, { read: boolean; write: boolean }>>) {
  const value = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess,
    modulesEnabled: new Set(),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

describe("useVisibleColumns", () => {
  it("throws outside a PermissionProvider", () => {
    const view = { name: "v", type: "list" as const, resource: "contacts.contact", label: "Contacts", columns: [] };
    expect(() => renderHook(() => useVisibleColumns(view))).toThrow(/PermissionProvider/);
  });

  it("returns only the columns the current user can read", () => {
    const view = {
      name: "v",
      type: "list" as const,
      resource: "contacts.contact",
      label: "Contacts",
      columns: [{ field: "name" }, { field: "ssn" }],
    };
    const { result } = renderHook(() => useVisibleColumns(view), {
      wrapper: wrapperWithFieldAccess({
        "contacts.contact": {
          name: { read: true, write: true },
          ssn: { read: false, write: false },
        },
      }),
    });

    expect(result.current.columns).toEqual([{ field: "name" }]);
  });

  it("lists readable hidden columns as toggle candidates, excluded from columns until revealed", () => {
    const view = {
      name: "v",
      type: "list" as const,
      resource: "contacts.contact",
      label: "Contacts",
      columns: [{ field: "name" }, { field: "internal_note", hidden: true }, { field: "ssn", hidden: true }],
    };
    const { result } = renderHook(() => useVisibleColumns(view), {
      wrapper: wrapperWithFieldAccess({
        "contacts.contact": {
          name: { read: true, write: true },
          internal_note: { read: true, write: true },
          ssn: { read: false, write: false },
        },
      }),
    });

    expect(result.current.columns).toEqual([{ field: "name" }]);
    expect(result.current.hiddenColumns).toEqual([{ field: "internal_note", hidden: true }]);
    expect(result.current.revealedFields.size).toBe(0);
  });

  it("toggleColumn reveals, then re-hides, a hidden column's field", () => {
    const view = {
      name: "v",
      type: "list" as const,
      resource: "contacts.contact",
      label: "Contacts",
      columns: [{ field: "name" }, { field: "internal_note", hidden: true }],
    };
    const { result } = renderHook(() => useVisibleColumns(view), {
      wrapper: wrapperWithFieldAccess({
        "contacts.contact": {
          name: { read: true, write: true },
          internal_note: { read: true, write: true },
        },
      }),
    });

    act(() => result.current.toggleColumn("internal_note"));
    expect(result.current.columns).toEqual([{ field: "name" }, { field: "internal_note", hidden: true }]);
    expect(result.current.revealedFields.has("internal_note")).toBe(true);

    act(() => result.current.toggleColumn("internal_note"));
    expect(result.current.columns).toEqual([{ field: "name" }]);
    expect(result.current.revealedFields.has("internal_note")).toBe(false);
  });
});

describe("useVisibleColumns conditions", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    resetReportedConditionErrors();
  });

  function wrapperWithPermissions(permissions: string[]) {
    const value = createPermissionContextValue({
      permissions: new Set(permissions),
      fieldAccess: { "contacts.contact": { name: { read: true, write: true }, margin: { read: true, write: true } } },
      modulesEnabled: new Set(),
    });
    return function Wrapper({ children }: { children: ReactNode }) {
      return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
    };
  }

  const view = (columns: ListColumn[]) => ({
    name: "contacts_list",
    type: "list" as const,
    resource: "contacts.contact",
    label: "Contacts",
    columns,
  });
  const marginCondition = "user_has_permission('contacts:contact:financials_read')";

  it("keeps a column whose condition holds", () => {
    const { result } = renderHook(
      () => useVisibleColumns(view([{ field: "name" }, { field: "margin", condition: marginCondition }])),
      { wrapper: wrapperWithPermissions(["contacts:contact:financials_read"]) },
    );
    expect(result.current.columns.map((c) => c.field)).toEqual(["name", "margin"]);
  });

  it("drops a column whose condition is false", () => {
    const { result } = renderHook(
      () => useVisibleColumns(view([{ field: "name" }, { field: "margin", condition: marginCondition }])),
      { wrapper: wrapperWithPermissions([]) },
    );
    expect(result.current.columns.map((c) => c.field)).toEqual(["name"]);
  });

  it("leaves a conditioned hidden column out of the toggle menu when its condition is false", () => {
    const { result } = renderHook(
      () => useVisibleColumns(view([{ field: "margin", hidden: true, condition: marginCondition }])),
      { wrapper: wrapperWithPermissions([]) },
    );
    expect(result.current.hiddenColumns).toEqual([]);
  });

  it("offers a conditioned hidden column in the toggle menu when its condition holds", () => {
    const { result } = renderHook(
      () => useVisibleColumns(view([{ field: "margin", hidden: true, condition: marginCondition }])),
      { wrapper: wrapperWithPermissions(["contacts:contact:financials_read"]) },
    );
    expect(result.current.hiddenColumns.map((c) => c.field)).toEqual(["margin"]);
  });

  it("treats a record reference as unbound, so the column is dropped", () => {
    const { result } = renderHook(
      () => useVisibleColumns(view([{ field: "name" }, { field: "margin", condition: "record.type = 'company'" }])),
      { wrapper: wrapperWithPermissions([]) },
    );
    expect(result.current.columns.map((c) => c.field)).toEqual(["name"]);
  });

  it("drops a column, and reports the view and column, when its condition is malformed", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    const { result } = renderHook(
      () => useVisibleColumns(view([{ field: "name" }, { field: "margin", condition: "user_has_permission(" }])),
      { wrapper: wrapperWithPermissions([]) },
    );
    expect(result.current.columns.map((c) => c.field)).toEqual(["name"]);
    const message = String(consoleError.mock.calls[0]?.[0]);
    expect(message).toContain("contacts_list");
    expect(message).toContain('column "margin" condition');
  });
});
