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

    it("returns false for a model/field pair with no security rule", () => {
      expect(value.checkField("contacts.contact", "name", "read")).toBe(false);
    });

    it("returns false for an unknown model", () => {
      expect(value.checkField("hr.employee", "salary_amount", "read")).toBe(false);
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
