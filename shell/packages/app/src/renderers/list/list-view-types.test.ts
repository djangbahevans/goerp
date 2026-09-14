import { AlertDialogInputSchema } from "@goerp/sdk/components";
import * as v from "valibot";
import { describe, expect, it } from "vitest";
import {
  BulkActionConfirmInputSchema,
  ListActionSchema,
  ListColumnSchema,
  ListFilterSchema,
  ListViewDeclarationSchema,
} from "./list-view-types.js";

describe("ListColumnSchema", () => {
  it("only requires field", () => {
    expect(v.safeParse(ListColumnSchema, { field: "email" }).success).toBe(true);
  });

  it("rejects a type outside the documented column-type enum", () => {
    expect(v.safeParse(ListColumnSchema, { field: "x", type: "text" }).success).toBe(true);
    expect(v.safeParse(ListColumnSchema, { field: "x", type: "money" }).success).toBe(false);
  });

  it("accepts manifest-spec.md §9.1's own canonical example verbatim, explicit nulls included", () => {
    const result = v.safeParse(ListColumnSchema, {
      field: "display_name",
      label: "Name",
      type: "text",
      sortable: true,
      width: null,
      min_width: null,
      max_width: null,
      truncate: true,
      align: "left",
      primary: false,
      hidden: false,
      href: null,
      condition: null,
      badge_config: null,
      avatar_field: null,
    });
    expect(result.success).toBe(true);
    expect(result.success && result.output.width).toBeUndefined();
  });
});

describe("BulkActionConfirmInputSchema", () => {
  it("extends AlertDialogInputSchema's own fields rather than retyping them", () => {
    const result = v.safeParse(BulkActionConfirmInputSchema, { label: "Reason", type: "text", field: "reason" });
    expect(result.success).toBe(true);
  });

  it("stays assignable to AlertDialogInput with no cast needed", () => {
    const parsed = v.parse(BulkActionConfirmInputSchema, { label: "Reason", type: "text", field: "reason" });
    // A type-level check as much as a runtime one: this line wouldn't
    // compile if BulkActionConfirmInput drifted from AlertDialogInput's
    // own shape (see bulk-actions.tsx, which relies on exactly this).
    expect(v.is(AlertDialogInputSchema, parsed)).toBe(true);
  });
});

describe("ListFilterSchema", () => {
  const base = { field: "state", label: "Status", type: "select" };

  it("requires field/label/type", () => {
    expect(v.safeParse(ListFilterSchema, base).success).toBe(true);
    expect(v.safeParse(ListFilterSchema, { field: "state", type: "select" }).success).toBe(false);
  });

  it("keeps an unknown field intact rather than stripping it", () => {
    const result = v.safeParse(ListFilterSchema, { ...base, future_field: 1 });
    expect(result.success).toBe(true);
    expect(result.success && result.output).toMatchObject({ future_field: 1 });
  });
});

describe("ListActionSchema", () => {
  it("rejects a type outside the documented action-type enum", () => {
    expect(v.safeParse(ListActionSchema, { label: "New", type: "create" }).success).toBe(true);
    expect(v.safeParse(ListActionSchema, { label: "New", type: "delete" }).success).toBe(false);
  });
});

describe("ListViewDeclarationSchema", () => {
  const base = { name: "contacts_list", type: "list", resource: "contacts.contact", label: "Contacts" };

  it("only requires the common view fields — everything else is optional", () => {
    expect(v.safeParse(ListViewDeclarationSchema, base).success).toBe(true);
  });

  it("rejects a type mismatched against the literal 'list' discriminant", () => {
    expect(v.safeParse(ListViewDeclarationSchema, { ...base, type: "kanban" }).success).toBe(false);
  });

  it("validates nested columns/filters/actions/bulk_actions", () => {
    const result = v.safeParse(ListViewDeclarationSchema, {
      ...base,
      columns: [{ field: "display_name", primary: true }],
      filters: [{ field: "state", label: "Status", type: "select" }],
      actions: [{ label: "New", type: "create", view: "contacts_form" }],
      bulk_actions: [{ label: "Archive", type: "route", route: "contacts.archive" }],
    });
    expect(result.success).toBe(true);
  });

  it("rejects a malformed nested column instead of silently accepting it", () => {
    const result = v.safeParse(ListViewDeclarationSchema, { ...base, columns: [{ label: "no field" }] });
    expect(result.success).toBe(false);
  });
});
