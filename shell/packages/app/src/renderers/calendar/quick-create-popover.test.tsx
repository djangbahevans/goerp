import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { QuickCreatePopover } from "./quick-create-popover.js";

afterEach(cleanup);

describe("QuickCreatePopover", () => {
  it("shows the pre-filled date", () => {
    render(
      <QuickCreatePopover
        date={new Date(2026, 4, 10)}
        onCreate={vi.fn()}
        onClose={vi.fn()}
        triggerRef={createRef<HTMLElement>()}
      />,
    );
    expect(screen.getByText("May 10, 2026")).toBeTruthy();
  });

  it("calls onCreate when the Create button is clicked", () => {
    const onCreate = vi.fn();
    render(
      <QuickCreatePopover
        date={new Date(2026, 4, 10)}
        onCreate={onCreate}
        onClose={vi.fn()}
        triggerRef={createRef<HTMLElement>()}
      />,
    );
    fireEvent.click(screen.getByText("Create"));
    expect(onCreate).toHaveBeenCalled();
  });

  it("focuses the Create button on mount", () => {
    render(
      <QuickCreatePopover
        date={new Date(2026, 4, 10)}
        onCreate={vi.fn()}
        onClose={vi.fn()}
        triggerRef={createRef<HTMLElement>()}
      />,
    );
    expect(document.activeElement).toBe(screen.getByText("Create"));
  });

  it("closes and returns focus to the trigger on Escape", () => {
    const onClose = vi.fn();
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    const triggerRef = { current: trigger };

    render(
      <QuickCreatePopover date={new Date(2026, 4, 10)} onCreate={vi.fn()} onClose={onClose} triggerRef={triggerRef} />,
    );
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

    expect(onClose).toHaveBeenCalled();
    expect(document.activeElement).toBe(trigger);
    trigger.remove();
  });
});
