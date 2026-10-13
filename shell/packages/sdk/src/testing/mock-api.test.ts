import { describe, expect, it, vi } from "vitest";
import { AppError } from "../error/app-error.js";
import { createMockApi } from "./mock-api.js";

interface TestApi {
  listContacts: (limit?: number) => Promise<{ id: string; name: string }[]>;
  getContact: (id: string) => Promise<{ id: string; name: string }>;
}

const create = createMockApi as (module: never, overrides?: Partial<TestApi>) => TestApi;

describe("createMockApi", () => {
  it("retains override identity, arguments and return values", async () => {
    const listContacts = vi.fn<TestApi["listContacts"]>().mockResolvedValue([{ id: "c1", name: "Ada" }]);
    const api = create("contacts" as never, { listContacts });
    expect(api.listContacts).toBe(listContacts);
    await expect(api.listContacts(10)).resolves.toEqual([{ id: "c1", name: "Ada" }]);
    expect(listContacts).toHaveBeenCalledWith(10);
  });

  it("rejects every un-overridden function with a named AppError", async () => {
    const api = create("contacts" as never);
    expect(api.getContact).toBe(api.getContact);
    await expect(api.getContact("c1")).rejects.toBeInstanceOf(AppError);
    await expect(api.getContact("c1")).rejects.toMatchObject({
      code: "testing.unmocked_request",
      httpStatus: 501,
      message: "Unmocked API function: contacts.getContact",
    });
    await expect(api.listContacts()).rejects.toMatchObject({ code: "testing.unmocked_request" });
  });

  it("can be awaited without inventing a then function", async () => {
    const api = create("contacts" as never);
    await expect(Promise.resolve(api)).resolves.toBe(api);
  });
});
