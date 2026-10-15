import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { toast } from "../notifications/toast.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { modalManager } from "./modal-manager.js";
import { handleUIPush, useRecordUIChannel } from "./ui-channels.js";
import type { UIInstruction } from "./ui-executor.js";

function push(channel: string, instructions: UIInstruction[]) {
  simulateRealtimeMessage(channel, { type: "custom", event: "ui.push", payload: { instructions } });
}

afterEach(() => {
  cleanup();
  toast.dismiss();
  vi.restoreAllMocks();
});

describe("handleUIPush", () => {
  it("runs a ui.push message's instructions, skipping the types a push can't carry", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const info = vi.spyOn(toast, "info");

    handleUIPush({
      channel: "ui:user:u1",
      type: "ui.push",
      payload: {
        instructions: [
          { type: "navigate", path: "/sales" },
          { type: "open_dialog", title: "Pay", action: { method: "POST", path: "/pay" }, fields: [] },
          { type: "toast", message: "Stock low", severity: "info" },
        ],
      },
    });

    await vi.waitFor(() => expect(info).toHaveBeenCalledWith("Stock low", { duration: 4000 }));
    expect(warn).toHaveBeenCalledWith("ui: navigate can't be pushed; skipped");
    expect(warn).toHaveBeenCalledWith("ui: open_dialog can't be pushed; skipped");
    expect(modalManager.getStack()).toEqual([]);
  });

  it("ignores other message types on the channel", () => {
    const info = vi.spyOn(toast, "info");
    handleUIPush({
      channel: "ui:user:u1",
      type: "role.changed",
      payload: { instructions: [{ type: "toast", message: "x", severity: "info" }] },
    });
    expect(info).not.toHaveBeenCalled();
  });

  it("warns about a ui.push without an instructions array", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    handleUIPush({ channel: "ui:user:u1", type: "ui.push", payload: { instructions: "toast" } });
    expect(warn).toHaveBeenCalledWith("ui: ui.push on ui:user:u1 has no instructions array; skipped");
  });
});

describe("useRecordUIChannel", () => {
  const toastFor = (message: string): UIInstruction[] => [{ type: "toast", message, severity: "info" }];

  it("runs pushes on the shown record's channel and releases it on a record switch and unmount", async () => {
    const info = vi.spyOn(toast, "info");
    const { rerender, unmount } = renderHook(({ id }) => useRecordUIChannel("sales.invoice", id), {
      initialProps: { id: "01j" as string | undefined },
    });

    push("ui:record:sales.invoice:01j", toastFor("first"));
    await vi.waitFor(() => expect(info).toHaveBeenCalledWith("first", { duration: 4000 }));

    rerender({ id: "02k" });
    push("ui:record:sales.invoice:01j", toastFor("stale"));
    push("ui:record:sales.invoice:02k", toastFor("second"));
    await vi.waitFor(() => expect(info).toHaveBeenCalledWith("second", { duration: 4000 }));

    unmount();
    push("ui:record:sales.invoice:02k", toastFor("after unmount"));
    await Promise.resolve();
    expect(info.mock.calls.map(([message]) => message)).toEqual(["first", "second"]);
  });

  it("subscribes to nothing for a new-record form", async () => {
    const info = vi.spyOn(toast, "info");
    renderHook(() => useRecordUIChannel("sales.invoice", undefined));
    push("ui:record:sales.invoice:undefined", toastFor("none"));
    await Promise.resolve();
    expect(info).not.toHaveBeenCalled();
  });
});
