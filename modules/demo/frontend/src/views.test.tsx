import { renderView, screen } from "@goerp/shell-app/testing";
import { expect, test } from "vitest";
import type { Item } from "./api/generated";

const item: Item = {
  id: "item-1",
  tenant_id: "tenant-1",
  name: "Demo item",
  created_at: "2026-10-14T00:00:00Z",
  updated_at: "2026-10-14T00:00:00Z",
  deleted_at: null,
  created_by: null,
  etag: "etag-1",
};

test("the declared item list renders its generated API mock", async () => {
  await renderView("demo.demo_item_list", {
    api: { listItems: async () => ({ data: [item], meta: { cursor: null, hasMore: false } }) },
    apiMocks: { "GET /_meta/saved-filters": { data: [] } },
  });
  expect(screen.getByText("Demo item")).toBeTruthy();
});
