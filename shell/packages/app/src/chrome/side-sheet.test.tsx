import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { type ReactNode, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SideSheet } from "./side-sheet.js";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function Harness({ initiallyOpen = false }: { initiallyOpen?: boolean }): ReactNode {
  const [open, setOpen] = useState(initiallyOpen);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open
      </button>
      <button type="button">Elsewhere</button>
      <SideSheet open={open} onClose={() => setOpen(false)} title="Sheet">
        body
      </SideSheet>
    </>
  );
}

describe("SideSheet", () => {
  it("returns focus to its trigger on close", () => {
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Open" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(document.activeElement).toBe(trigger);
  });

  it("returns focus on close when mounted already open", () => {
    const elsewhere = document.createElement("button");
    document.body.append(elsewhere);
    elsewhere.focus();
    render(<Harness initiallyOpen />);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(document.activeElement).toBe(elsewhere);
    elsewhere.remove();
  });

  it("doesn't take focus back once its exit animation ends", () => {
    vi.useFakeTimers();
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Open" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    const elsewhere = screen.getByRole("button", { name: "Elsewhere" });
    elsewhere.focus();
    act(() => vi.advanceTimersByTime(200));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(elsewhere);
  });
});
