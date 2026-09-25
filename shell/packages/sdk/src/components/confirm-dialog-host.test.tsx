import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ConfirmQueue, confirmQueue, useConfirm } from "../react/use-confirm.js";
import { ConfirmDialogHost } from "./confirm-dialog-host.js";

afterEach(cleanup);

function renderHost() {
  const queue = new ConfirmQueue();
  render(<ConfirmDialogHost queue={queue} />);
  const ask = (options: Parameters<ConfirmQueue["confirm"]>[0]) => {
    let answer!: Promise<boolean>;
    act(() => {
      answer = queue.confirm(options);
    });
    return answer;
  };
  return { queue, ask };
}

const ARCHIVE = {
  title: "Archive this contact?",
  description: "Kwame Mensah will be hidden from all lists.",
  confirmLabel: "Archive",
  variant: "danger" as const,
};

describe("ConfirmDialogHost", () => {
  it("renders nothing with no request", () => {
    renderHost();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("resolves true on confirm", async () => {
    const { ask } = renderHost();
    const answer = ask(ARCHIVE);

    fireEvent.click(await screen.findByRole("button", { name: "Archive" }));

    await expect(answer).resolves.toBe(true);
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("resolves false on cancel", async () => {
    const { ask } = renderHost();
    const answer = ask(ARCHIVE);

    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));

    await expect(answer).resolves.toBe(false);
  });

  it("resolves false on Escape", async () => {
    const { ask } = renderHost();
    const answer = ask(ARCHIVE);

    fireEvent.keyDown(await screen.findByRole("alertdialog"), { key: "Escape" });

    await expect(answer).resolves.toBe(false);
  });

  it("uses the given labels, defaulting to Confirm and Cancel", async () => {
    const { ask } = renderHost();
    ask({ title: "Continue?", description: "Unsaved changes will be lost.", cancelLabel: "Stay" });

    expect(await screen.findByRole("button", { name: "Confirm" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stay" })).toBeTruthy();
  });

  it.each([
    ["danger", "bg-danger", "lucide-circle-alert"],
    ["warning", "bg-primary", "lucide-triangle-alert"],
    ["default", "bg-primary", null],
  ] as const)("styles the %s variant", async (variant, buttonClass, iconClass) => {
    const { ask } = renderHost();
    ask({ title: "Go ahead?", description: "Details.", confirmLabel: "Go", variant });

    const dialog = await screen.findByRole("alertdialog");
    expect(screen.getByRole("button", { name: "Go" }).className).toContain(buttonClass);
    if (iconClass) {
      await waitFor(() => expect(dialog.querySelector(`.${iconClass}`)).not.toBeNull());
    } else {
      expect(dialog.querySelector("svg.lucide")).toBeNull();
    }
  });

  it("keeps confirm disabled until the exact phrase is typed", async () => {
    const { ask } = renderHost();
    const answer = ask({ ...ARCHIVE, confirmLabel: "Delete permanently", requireTyping: "DELETE" });
    let settled = false;
    void answer.then(() => {
      settled = true;
    });

    const confirm = await screen.findByRole("button", { name: "Delete permanently" });
    const phrase = screen.getByLabelText("Type DELETE to confirm");
    expect(confirm.hasAttribute("disabled")).toBe(true);

    fireEvent.change(phrase, { target: { value: "delete" } });
    expect(confirm.hasAttribute("disabled")).toBe(true);
    fireEvent.change(phrase, { target: { value: "DELETE " } });
    expect(confirm.hasAttribute("disabled")).toBe(true);
    fireEvent.click(confirm);
    await Promise.resolve();
    expect(settled).toBe(false);

    fireEvent.change(phrase, { target: { value: "DELETE" } });
    expect(confirm.hasAttribute("disabled")).toBe(false);
    fireEvent.click(confirm);

    await expect(answer).resolves.toBe(true);
  });

  it("shows a request made while another is open once the first is answered", async () => {
    const { ask } = renderHost();
    const first = ask({ ...ARCHIVE, requireTyping: "ARCHIVE" });
    const second = ask({ title: "Delete this contact?", description: "Gone for good.", confirmLabel: "Delete" });

    expect(await screen.findByText("Archive this contact?")).toBeTruthy();
    expect(screen.queryByText("Delete this contact?")).toBeNull();
    fireEvent.change(screen.getByLabelText("Type ARCHIVE to confirm"), { target: { value: "ARCHIVE" } });
    fireEvent.click(screen.getByRole("button", { name: "Archive" }));
    await expect(first).resolves.toBe(true);

    expect(await screen.findByText("Delete this contact?")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await expect(second).resolves.toBe(false);
  });

  it("returns focus to the element that opened the dialog", async () => {
    const { ask } = renderHost();
    const trigger = document.createElement("button");
    document.body.append(trigger);
    trigger.focus();

    ask(ARCHIVE);
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(document.activeElement).toBe(trigger));
    trigger.remove();
  });
});

describe("ConfirmQueue", () => {
  it("ignores an answer for a request that isn't showing", async () => {
    const queue = new ConfirmQueue();
    const first = queue.confirm({ title: "A", description: "a" });
    const second = queue.confirm({ title: "B", description: "b" });
    const secondId = 2;

    queue.settle(secondId, true);
    expect(queue.current()?.options.title).toBe("A");

    queue.settle(queue.current()!.id, false);
    await expect(first).resolves.toBe(false);
    expect(queue.current()?.id).toBe(secondId);
    queue.settle(secondId, true);
    await expect(second).resolves.toBe(true);
  });
});

describe("useConfirm", () => {
  it("returns the shared queue's confirm, stable across renders", () => {
    const { result, rerender } = renderHook(() => useConfirm());
    const first = result.current;
    rerender();

    expect(result.current).toBe(first);
    expect(result.current.confirm).toBe(confirmQueue.confirm);
  });
});
