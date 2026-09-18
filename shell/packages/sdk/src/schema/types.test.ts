import * as v from "valibot";
import { describe, expect, it } from "vitest";
import { FieldDefSchema, ModuleSchemaSchema, RouteSchemaSchema, WorkflowTransitionSchema } from "./types.js";

const baseRoute = { method: "GET", path: "/x", permissions: [], response_is_list: false };

describe("RouteSchemaSchema", () => {
  it("requires permissions to be present, but accepts null — Go's zero-value nil slice marshals as JSON null, not []", () => {
    expect(v.safeParse(RouteSchemaSchema, { ...baseRoute, permissions: null }).success).toBe(true);
    expect(v.safeParse(RouteSchemaSchema, { ...baseRoute, permissions: ["a:b"] }).success).toBe(true);
    const { permissions: _permissions, ...withoutPermissions } = baseRoute;
    expect(v.safeParse(RouteSchemaSchema, withoutPermissions).success).toBe(false);
  });

  it("rejects a crud_action outside the documented enum", () => {
    expect(v.safeParse(RouteSchemaSchema, { ...baseRoute, crud_action: "list" }).success).toBe(true);
    expect(v.safeParse(RouteSchemaSchema, { ...baseRoute, crud_action: "upsert" }).success).toBe(false);
  });

  it("name/model/view stay optional", () => {
    expect(v.safeParse(RouteSchemaSchema, baseRoute).success).toBe(true);
  });

  it("keeps a field this schema doesn't declare, rather than stripping it from a forward-compatible backend", () => {
    const result = v.safeParse(RouteSchemaSchema, { ...baseRoute, deprecated: true, rate_limit: 100 });
    expect(result.success).toBe(true);
    expect(result.success && result.output).toMatchObject({ deprecated: true, rate_limit: 100 });
  });
});

describe("FieldDefSchema", () => {
  it("only requires name and type", () => {
    expect(v.safeParse(FieldDefSchema, { name: "email", type: "text" }).success).toBe(true);
  });

  it("leaves workflow absent for a field with no .Workflow() declaration", () => {
    const result = v.safeParse(FieldDefSchema, { name: "state", type: "selection" });
    expect(result.success).toBe(true);
    expect(result.success && result.output.workflow).toBeUndefined();
  });

  it("parses a .Workflow()-declared field's states and transitions", () => {
    const result = v.safeParse(FieldDefSchema, {
      name: "state",
      type: "selection",
      workflow: {
        states: ["draft", "confirmed"],
        transitions: [{ from: "draft", to: "confirmed", action_name: "confirm", permission: "sales:order:confirm" }],
      },
    });
    expect(result.success).toBe(true);
    expect(result.success && result.output.workflow?.states).toEqual(["draft", "confirmed"]);
  });
});

describe("WorkflowTransitionSchema", () => {
  it("requires from/to/action_name; permission and condition stay optional", () => {
    expect(
      v.safeParse(WorkflowTransitionSchema, { from: "draft", to: "confirmed", action_name: "confirm" }).success,
    ).toBe(true);
    expect(v.safeParse(WorkflowTransitionSchema, { from: "draft", to: "confirmed" }).success).toBe(false);
  });

  it("carries condition through as a raw, unvalidated expression string", () => {
    const result = v.safeParse(WorkflowTransitionSchema, {
      from: "confirmed",
      to: "done",
      action_name: "complete",
      condition: "record.amount_paid >= record.amount_total",
    });
    expect(result.success).toBe(true);
    expect(result.success && result.output.condition).toBe("record.amount_paid >= record.amount_total");
  });
});

describe("ModuleSchemaSchema", () => {
  const baseModule = {
    name: "sales",
    version: "1.0.0",
    display_name: "Sales",
    routes: [],
    views: [],
    navigation: [],
    models: {},
    permissions: [],
    frontend: null,
    public_config: {},
  };

  it("accepts a null frontend bundle", () => {
    expect(v.safeParse(ModuleSchemaSchema, baseModule).success).toBe(true);
  });

  it("accepts a declared frontend bundle", () => {
    const withBundle = { ...baseModule, frontend: { bundle_url: "https://x", bundle_sha256: "abc" } };
    expect(v.safeParse(ModuleSchemaSchema, withBundle).success).toBe(true);
  });

  it("leaves views/navigation/permissions unvalidated — each renderer's own manifest-types.ts job", () => {
    const loose = { ...baseModule, views: [{ anything: "goes" }], navigation: ["x"], permissions: [1, 2] };
    expect(v.safeParse(ModuleSchemaSchema, loose).success).toBe(true);
  });
});
