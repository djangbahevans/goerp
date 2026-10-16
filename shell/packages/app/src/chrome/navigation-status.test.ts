import { describe, expect, it } from "vitest";
import { navigationStatus } from "./navigation-status.js";

describe("navigationStatus", () => {
  const checks = {
    moduleEnabled: (module: string) => module === "contacts",
    check: (permission: string) => permission === "contacts:contact:read",
    conditionHolds: (condition: string) => condition === "true",
  };

  it("shows an entry whose module, permission and condition all pass", () => {
    expect(
      navigationStatus({ module: "contacts", permission: "contacts:contact:read", condition: "true" }, checks),
    ).toEqual({ shown: true, reasons: [] });
  });

  it("names each missing entitlement, permission and failing condition", () => {
    expect(navigationStatus({ module: "hr", permission: "hr:employee:read", condition: "false" }, checks)).toEqual({
      shown: false,
      reasons: ['module "hr" is not entitled', 'missing permission "hr:employee:read"', "condition is false: false"],
    });
  });

  it("checks no entitlement for an item, which inherits its group's", () => {
    expect(navigationStatus({ permission: "contacts:contact:read" }, checks)).toEqual({ shown: true, reasons: [] });
  });
});
