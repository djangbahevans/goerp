import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockFetch } from "./mock-fetch.js";

afterEach(() => vi.restoreAllMocks());

describe("API mock matching", () => {
  it("matches module paths, ignores query strings and prefers exact paths", async () => {
    const fetch = createMockFetch({
      "GET /contacts/*": { name: "Wildcard" },
      "GET /contacts/c1": { name: "Exact" },
      "POST /contacts/*": { name: "Created" },
    });
    await expect((await fetch("/_m/contacts/c1?limit=10")).json()).resolves.toEqual({ name: "Exact" });
    await expect((await fetch("https://tenant.test/contacts/c2")).json()).resolves.toEqual({ name: "Wildcard" });
    await expect((await fetch("/contacts/c1", { method: "POST" })).json()).resolves.toEqual({ name: "Created" });
  });

  it("requires each wildcard to match exactly one nonempty segment", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const fetch = createMockFetch({ "GET /contacts/*/activities": [] });
    expect((await fetch("/contacts/c1/activities")).status).toBe(200);
    for (const path of ["/contacts//activities", "/contacts/c1/c2/activities", "/contacts/c1/activity"]) {
      const response = await fetch(path);
      expect(response.status).toBe(501);
      await expect(response.json()).resolves.toMatchObject({
        error: { code: "testing.unmocked_request", message: `Unmocked API request: GET ${path}` },
      });
      expect(error).toHaveBeenCalledWith(`Unmocked API request: GET ${path}`);
    }
  });

  it("reads URL and method from Request and accepts init overrides", async () => {
    const fetch = createMockFetch({ "POST /contacts": { created: true }, "DELETE /contacts": null });
    const request = new Request("http://testing.local/contacts", { method: "POST" });
    await expect((await fetch(request)).json()).resolves.toEqual({ created: true });
    expect((await fetch(request, { method: "DELETE" })).status).toBe(404);
    await expect((await fetch(new URL("http://testing.local/contacts"), { method: "POST" })).json()).resolves.toEqual({
      created: true,
    });
  });

  it("rejects aborted requests and malformed patterns", async () => {
    const fetch = createMockFetch({ "GET /contacts": [] });
    await expect(fetch("/contacts", { signal: AbortSignal.abort() })).rejects.toMatchObject({ name: "AbortError" });
    expect(() => createMockFetch({ "/contacts": [] })).toThrow("expected METHOD /path");
  });
});
