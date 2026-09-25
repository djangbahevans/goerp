import { describe, expect, it } from "vitest";
import { toRealtimeMessage } from "./realtime-message.js";

describe("toRealtimeMessage", () => {
  it("maps a record change's payload to camelCase fields", () => {
    const record = { id: "o1", total: 100 };
    expect(
      toRealtimeMessage({
        channel: "feed:sales",
        type: "record.updated",
        payload: { resource: "sales.order", record_id: "o1", record },
      }),
    ).toEqual({ type: "record.updated", event: "record.updated", resource: "sales.order", recordId: "o1", record });
  });

  it("leaves out fields a record change doesn't carry", () => {
    expect(toRealtimeMessage({ channel: "feed:sales", type: "record.deleted" })).toEqual({
      type: "record.deleted",
      event: "record.deleted",
    });
    expect(
      toRealtimeMessage({ channel: "feed:sales", type: "record.created", payload: { resource: 7, record_id: null } }),
    ).toEqual({ type: "record.created", event: "record.created" });
  });

  it("makes any other type a custom message that keeps its type and whole payload", () => {
    expect(
      toRealtimeMessage({ channel: "notifications", type: "notification.new", payload: { id: "n1", title: "Hi" } }),
    ).toEqual({ type: "custom", event: "notification.new", payload: { id: "n1", title: "Hi" } });
  });
});
