import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BulkActionContext, type BulkActionContextValue } from "../react/bulk-action-context.js";
import { BulkActionPanel } from "./bulk-action-panel.js";

afterEach(cleanup);

function contextValue(overrides: Partial<BulkActionContextValue> = {}): BulkActionContextValue {
  return {
    selectedIds: ["1", "2", "3"],
    selectedCount: 3,
    onComplete: vi.fn(),
    onCancel: vi.fn(),
    isLoading: false,
    setLoading: vi.fn(),
    ...overrides,
  };
}

function Provider({ value, children }: { value: BulkActionContextValue; children: ReactNode }) {
  return <BulkActionContext.Provider value={value}>{children}</BulkActionContext.Provider>;
}

describe("BulkActionPanel", () => {
  it("throws outside a BulkActionContext.Provider", () => {
    expect(() =>
      render(
        <BulkActionPanel>
          <button type="button">Add to 3 contacts</button>
        </BulkActionPanel>,
      ),
    ).toThrow(/useBulkAction must be used within/);
  });

  it("renders its children as a labeled modal dialog", () => {
    render(
      <Provider value={contextValue()}>
        <BulkActionPanel>
          <button type="button">Add to 3 contacts</button>
        </BulkActionPanel>
      </Provider>,
    );
    expect(screen.getByRole("dialog", { name: "Bulk action" })).toBeTruthy();
    expect(screen.getByText("Add to 3 contacts")).toBeTruthy();
  });

  it("calls the context's onCancel when Escape is pressed", async () => {
    const onCancel = vi.fn();
    render(
      <Provider value={contextValue({ onCancel })}>
        <BulkActionPanel>
          <button type="button">Add to 3 contacts</button>
        </BulkActionPanel>
      </Provider>,
    );
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await vi.waitFor(() => expect(onCancel).toHaveBeenCalled());
  });

  it("does not dismiss when the overlay backdrop is clicked", () => {
    const onCancel = vi.fn();
    render(
      <Provider value={contextValue({ onCancel })}>
        <BulkActionPanel>
          <button type="button">Add to 3 contacts</button>
        </BulkActionPanel>
      </Provider>,
    );
    fireEvent.pointerDown(document.body);
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("auto-focuses a focusable element inside the panel when mounted", async () => {
    render(
      <Provider value={contextValue()}>
        <BulkActionPanel>
          <button type="button">Add to 3 contacts</button>
        </BulkActionPanel>
      </Provider>,
    );
    await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByText("Add to 3 contacts")));
  });

  it("returns focus to the triggering element once unmounted", async () => {
    function Harness() {
      const [active, setActive] = useState(false);
      const onCancel = () => setActive(false);
      return (
        <>
          <button type="button" onClick={() => setActive(true)}>
            Add Tag
          </button>
          {active && (
            <Provider value={contextValue({ onCancel })}>
              <BulkActionPanel>
                <button type="button" onClick={onCancel}>
                  Cancel
                </button>
              </BulkActionPanel>
            </Provider>
          )}
        </>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Add Tag" });
    trigger.focus();
    fireEvent.click(trigger);
    await vi.waitFor(() => expect(screen.getByRole("dialog")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
  });
});
