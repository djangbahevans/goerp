import { describe, expect, it, vi } from "vitest";
import type { APIClient } from "../http/types.js";
import { fetchPermissions } from "./permission-client.js";

function fakeClient(body: unknown): Pick<APIClient, "get"> {
  return { get: vi.fn(async () => body) as Pick<APIClient, "get">["get"] };
}

describe("fetchPermissions", () => {
  it("maps the response into permission/module Sets and passes field_access through", async () => {
    const client = fakeClient({
      permissions: ["sales:order:read", "sales:order:confirm"],
      field_access: {
        "contacts.contact": { credit_limit: { read: false, write: false } },
      },
      modules_enabled: ["sales", "contacts"],
    });

    const data = await fetchPermissions(client);

    expect(data.permissions).toEqual(new Set(["sales:order:read", "sales:order:confirm"]));
    expect(data.modulesEnabled).toEqual(new Set(["sales", "contacts"]));
    expect(data.fieldAccess).toEqual({
      "contacts.contact": { credit_limit: { read: false, write: false } },
    });
    expect(client.get).toHaveBeenCalledWith("/_meta/permissions");
  });

  it("maps an empty response to empty Sets and an empty field_access map", async () => {
    const client = fakeClient({ permissions: [], field_access: {}, modules_enabled: [] });

    const data = await fetchPermissions(client);

    expect(data.permissions.size).toBe(0);
    expect(data.modulesEnabled.size).toBe(0);
    expect(data.fieldAccess).toEqual({});
  });
});
