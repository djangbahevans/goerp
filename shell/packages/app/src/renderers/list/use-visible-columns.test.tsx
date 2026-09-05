import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
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

    expect(result.current).toEqual([{ field: "name" }]);
  });
});
