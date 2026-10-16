import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { invalidateResourceLists } from "./resource-list-queries.js";

describe("invalidateResourceLists", () => {
  it("invalidates every cached list of the resource and leaves other queries fresh", async () => {
    const queryClient = new QueryClient();
    const resourceLists = [
      ["infinite-list", null, "contacts.contact", null, null, null],
      ["infinite-list", "embedded:01j:contacts", "contacts.contact", { status: "active" }, null, null],
      ["tree-children", "contacts.contact", "parent_id", "01j", null, null],
      ["list", "contacts.contact", null, null, 50, null],
    ];
    const untouched = [
      ["infinite-list", null, "contacts.company", null, null, null],
      ["list", "contacts.company", null, null, 50, null],
      ["record", "contacts.contact", "01j"],
    ];
    for (const queryKey of [...resourceLists, ...untouched]) queryClient.setQueryData(queryKey, {});

    await invalidateResourceLists(queryClient, "contacts.contact");

    for (const queryKey of resourceLists) expect(queryClient.getQueryState(queryKey)?.isInvalidated).toBe(true);
    for (const queryKey of untouched) expect(queryClient.getQueryState(queryKey)?.isInvalidated).toBe(false);
  });
});
