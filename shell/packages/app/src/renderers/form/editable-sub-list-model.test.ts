import { AppError } from "@goerp/sdk/error";
import type { ResourceRegistryEntry } from "@goerp/sdk/schema";
import { describe, expect, it, vi } from "vitest";
import {
  changedFields,
  editorFor,
  mapSaveError,
  resolveSubListRoutes,
  sendWrite,
  toSortParam,
} from "./editable-sub-list-model.js";

const entry: ResourceRegistryEntry = {
  module: "sales",
  resource: "sales.order_line",
  listPath: "/order-lines",
  getPath: "/order-lines/{id}",
  createPath: "/order-lines",
  updatePath: "/order-lines/{id}",
  deletePath: null,
  pivotPath: null,
  listMethod: "GET",
  createMethod: "POST",
  updateMethod: "PATCH",
  deleteMethod: null,
  createPermissions: ["sales:order:write"],
  updatePermissions: ["sales:order:write"],
  deletePermissions: null,
  previewPath: "/order-lines/preview",
};

describe("resolveSubListRoutes", () => {
  it("uses the model's CRUD routes, and null for one it doesn't declare", async () => {
    const routes = await resolveSubListRoutes({}, entry, { enabled_ops: ["list", "preview"] }, vi.fn());
    expect(routes.create).toEqual({ method: "POST", path: "/order-lines", permissions: ["sales:order:write"] });
    expect(routes.update?.method).toBe("PATCH");
    expect(routes.delete).toBeNull();
    expect(routes.previewPath).toBe("/order-lines/preview");
  });

  it("resolves a section's named route override instead", async () => {
    const resolve = vi.fn(async () => ({ method: "DELETE", path: "/lines/{id}", permissions: ["sales:line:delete"] }));
    const routes = await resolveSubListRoutes(
      { delete_route: "sales.deleteLine" },
      entry,
      { enabled_ops: [] },
      resolve,
    );
    expect(resolve).toHaveBeenCalledWith("sales.deleteLine");
    expect(routes.delete).toEqual({ method: "DELETE", path: "/lines/{id}", permissions: ["sales:line:delete"] });
  });

  it("drops the preview path when the model doesn't enable preview", async () => {
    const routes = await resolveSubListRoutes({}, entry, { enabled_ops: ["list"] }, vi.fn());
    expect(routes.previewPath).toBeNull();
  });
});

describe("sendWrite", () => {
  it("fills {id} and sends the route's method", async () => {
    const client = { post: vi.fn(), put: vi.fn(), patch: vi.fn(async () => ({ id: "l1" })), delete: vi.fn() };
    await sendWrite(
      client as unknown as Parameters<typeof sendWrite>[0],
      { method: "PATCH", path: "/order-lines/{id}", permissions: [] },
      "l1",
      { qty: 2 },
    );
    expect(client.patch).toHaveBeenCalledWith("/order-lines/l1", { qty: 2 });
  });
});

describe("toSortParam", () => {
  it("maps the manifest's sort spelling to the list API's", () => {
    expect(toSortParam("sequence ASC")).toBe("sequence");
    expect(toSortParam("sequence DESC")).toBe("-sequence");
    expect(toSortParam("name")).toBe("name");
    expect(toSortParam(undefined)).toBeUndefined();
  });
});

describe("editorFor", () => {
  it("maps column types to field editors", () => {
    expect(editorFor({ field: "qty", type: "number" })).toEqual({
      kind: "field",
      field: { field: "qty", label: "qty", type: "number" },
    });
    expect(editorFor({ field: "state", type: "badge", badge_config: { draft: { label: "Draft" } } })).toEqual({
      kind: "field",
      field: { field: "state", label: "state", type: "select", options: [{ value: "draft", label: "Draft" }] },
    });
    expect(editorFor({ field: "product_id", type: "relation", resource: "inventory.product" })).toMatchObject({
      kind: "field",
      field: { type: "select", resource: "inventory.product" },
    });
    expect(editorFor({ field: "active", type: "boolean" })).toEqual({ kind: "checkbox" });
    expect(editorFor({ field: "labels", type: "tags" })).toEqual({ kind: "tags" });
  });

  it("gives no editor to display-only types, or a relation with no resource", () => {
    for (const type of ["relative_time", "avatar", "json", "custom", "file"] as const) {
      expect(editorFor({ field: "x", type })).toBeNull();
    }
    expect(editorFor({ field: "product_id", type: "relation" })).toBeNull();
  });
});

describe("mapSaveError", () => {
  const editable = new Set(["qty", "price_unit"]);
  const labelOf = (field: string) => (field === "tax_id" ? "Tax" : field);

  it("puts a 422's per-field messages in their cells, and the rest on the row", () => {
    const err = new AppError({
      code: "validation_failed",
      message: "some fields are invalid",
      httpStatus: 422,
      details: { qty: ["Must be positive"], tax_id: "Unknown tax" },
    });
    expect(mapSaveError(err, editable, labelOf)).toEqual({
      cells: { qty: "Must be positive" },
      row: "Tax: Unknown tax",
    });
  });

  it("maps an orm.validation_failed details.field to that cell", () => {
    const err = new AppError({
      code: "orm.validation_failed",
      message: "Quantity exceeds stock",
      httpStatus: 422,
      details: { field: "qty" },
    });
    expect(mapSaveError(err, editable, labelOf)).toEqual({ cells: { qty: "Quantity exceeds stock" }, row: null });
  });

  it("explains a conflict on the row", () => {
    const err = new AppError({ code: "orm.etag_mismatch", message: "stale", httpStatus: 409 });
    expect(mapSaveError(err, editable, labelOf).row).toBe("This row changed since you opened it. Cancel to reload it.");
  });

  it("shows any other failure's message on the row", () => {
    expect(mapSaveError(new Error("Network down"), editable, labelOf)).toEqual({ cells: {}, row: "Network down" });
  });
});

describe("changedFields", () => {
  it("keeps only edits that differ from the baseline", () => {
    expect(changedFields({ qty: 1, name: "a" }, { qty: 1, name: "b" })).toEqual({ name: "b" });
  });
});
