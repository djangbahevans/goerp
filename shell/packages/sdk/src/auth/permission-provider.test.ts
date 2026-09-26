import { describe, expect, it } from "vitest";
import { createPermissionContextValue } from "./permission-provider.js";
import type { PermissionData } from "./permission-types.js";

const data: PermissionData = {
  permissions: new Set(["sales:order:read", "sales:order:confirm"]),
  fieldAccess: {
    "contacts.contact": {
      credit_limit: { read: false, write: false },
      tax_id: { read: true, write: false },
    },
  },
  modulesEnabled: new Set(["sales"]),
};

describe("createPermissionContextValue", () => {
  const value = createPermissionContextValue(data);

  describe("check", () => {
    it("returns true for a granted permission", () => {
      expect(value.check("sales:order:read")).toBe(true);
    });

    it("returns false for a permission not in the set", () => {
      expect(value.check("sales:order:delete")).toBe(false);
    });

    it("ignores resourceId — no per-record ABAC data is pre-loaded to evaluate it against", () => {
      expect(value.check("sales:order:read", "some-order-id")).toBe(true);
      expect(value.check("sales:order:delete", "some-order-id")).toBe(false);
    });
  });

  describe("checkField", () => {
    it("returns the read/write flags for a known model/field pair", () => {
      expect(value.checkField("contacts.contact", "tax_id", "read")).toBe(true);
      expect(value.checkField("contacts.contact", "tax_id", "write")).toBe(false);
      expect(value.checkField("contacts.contact", "credit_limit", "read")).toBe(false);
    });

    it("allows a field with no security rule, since field_access lists only declared rules", () => {
      expect(value.checkField("contacts.contact", "name", "read")).toBe(true);
      expect(value.checkField("contacts.contact", "name", "write")).toBe(true);
    });

    it("allows the fields of a model with no security rules", () => {
      expect(value.checkField("hr.employee", "salary_amount", "read")).toBe(true);
    });

    it("denies fields with no security rule while the data is pending", () => {
      const pending = createPermissionContextValue({ ...data, pending: true });
      expect(pending.checkField("contacts.contact", "name", "read")).toBe(false);
      expect(pending.checkField("contacts.contact", "tax_id", "read")).toBe(true);
    });
  });

  describe("moduleEnabled", () => {
    it("returns true for an enabled module", () => {
      expect(value.moduleEnabled("sales")).toBe(true);
    });

    it("returns false for a module not enabled for this tenant", () => {
      expect(value.moduleEnabled("hr")).toBe(false);
    });
  });
});
