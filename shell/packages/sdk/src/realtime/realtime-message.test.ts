import { describe, expect, it } from "vitest";
import { isRecordChange, type RealtimeMessage, toRealtimeMessage } from "./realtime-message.js";

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

  it("decodes terminal identifiers and discards every extra payload field", () => {
    expect(
      toRealtimeMessage({
        channel: "record:sales.order:o1",
        type: "record.unavailable",
        payload: { resource: "sales.order", record_id: "o1", record: { secret: "private" }, reason: "deleted" },
      }),
    ).toEqual({ type: "record.unavailable", event: "record.unavailable", resource: "sales.order", recordId: "o1" });
  });

  it("makes any other type a custom message that keeps its type and whole payload", () => {
    expect(
      toRealtimeMessage({ channel: "notifications", type: "notification.new", payload: { id: "n1", title: "Hi" } }),
    ).toEqual({ type: "custom", event: "notification.new", payload: { id: "n1", title: "Hi" } });
  });
});

describe("isRecordChange", () => {
  it.each(["record.created", "record.updated", "record.deleted", "record.unavailable"] as const)(
    "accepts %s with a resource and id",
    (type) => {
      const message: RealtimeMessage = { type, event: type, resource: "sales.order", recordId: "o1" };
      expect(isRecordChange(message, "sales.order")).toBe(true);
      expect(isRecordChange(message, "sales.invoice")).toBe(false);
      expect(isRecordChange({ ...message, recordId: undefined } as unknown as RealtimeMessage, "sales.order")).toBe(
        false,
      );
    },
  );

  it("rejects custom messages even with matching resource and id", () => {
    expect(
      isRecordChange({ type: "custom", event: "notice", resource: "sales.order", recordId: "o1" }, "sales.order"),
    ).toBe(false);
  });
});
