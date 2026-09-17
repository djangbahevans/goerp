import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { IconPicker } from "./icon-picker.js";

// @tanstack/react-virtual measures its scroll container via
// element.offsetWidth/offsetHeight (virtual-core's own getRect) and, for
// updates, a ResizeObserver — neither exists in jsdom by default, so every
// row range comes back empty without this. The first component in this
// codebase needing it; scoped to this file only.
beforeEach(() => {
  Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, value: 320 });
  Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, value: 320 });
  globalThis.ResizeObserver = class {
    callback: ResizeObserverCallback;
    constructor(callback: ResizeObserverCallback) {
      this.callback = callback;
    }
    observe(target: Element) {
      this.callback(
        [{ target, contentRect: { width: 320, height: 320 } } as unknown as ResizeObserverEntry],
        this as unknown as ResizeObserver,
      );
    }
    unobserve() {}
    disconnect() {}
  };
});

afterEach(() => {
  vi.restoreAllMocks();
  cleanup();
});

function open(): HTMLElement {
  const input = screen.getByRole("combobox");
  fireEvent.focus(input);
  return input;
}

describe("IconPicker", () => {
  it("shows the placeholder when nothing is selected", () => {
    render(<IconPicker value="" onChange={vi.fn()} placeholder="Choose an icon…" />);
    expect((screen.getByRole("combobox") as HTMLInputElement).placeholder).toBe("Choose an icon…");
  });

  it("shows the selected icon's name in the trigger when closed", () => {
    render(<IconPicker value="shopping-cart" onChange={vi.fn()} />);
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("shopping-cart");
  });

  it("opens on focus and renders a role=grid of gridcells, not the full ~2000-icon set at once", () => {
    render(<IconPicker value="" onChange={vi.fn()} />);
    open();
    expect(screen.getByRole("grid")).toBeTruthy();
    const cells = document.querySelectorAll('[role="gridcell"]');
    expect(cells.length).toBeGreaterThan(0);
    expect(cells.length).toBeLessThan(200);
  });

  it("filters by substring match against the icon name", () => {
    render(<IconPicker value="" onChange={vi.fn()} />);
    const input = open();
    // "shopping-cart-minus"/"shopping-cart-plus" also legitimately contain
    // this substring — the filter is substring match, not exact match.
    fireEvent.change(input, { target: { value: "shopping-cart" } });
    const labels = Array.from(document.querySelectorAll('[role="gridcell"]')).map((c) => c.getAttribute("aria-label"));
    expect(labels).toEqual(["shopping-cart", "shopping-cart-minus", "shopping-cart-plus"]);
  });

  it("filters by a substring that only matches inside a hyphenated segment", () => {
    render(<IconPicker value="" onChange={vi.fn()} />);
    const input = open();
    fireEvent.change(input, { target: { value: "cart" } });
    const cells = document.querySelectorAll('[role="gridcell"]');
    const labels = Array.from(cells).map((c) => c.getAttribute("aria-label"));
    expect(labels).toContain("shopping-cart");
  });

  it("shows a 'no results' row, announced via aria-live, when the query matches nothing", () => {
    render(<IconPicker value="" onChange={vi.fn()} />);
    const input = open();
    fireEvent.change(input, { target: { value: "zzznotarealiconzzz" } });
    const empty = screen.getByText('No results for "zzznotarealiconzzz"');
    expect(empty.getAttribute("aria-live")).toBe("polite");
    expect(document.querySelectorAll('[role="gridcell"]').length).toBe(0);
    // aria-controls must resolve to a real element in every state, not just
    // when the grid itself is rendered.
    expect(empty.id).toBe(input.getAttribute("aria-controls"));
  });

  it("commits the clicked icon and closes", () => {
    const onChange = vi.fn();
    render(<IconPicker value="" onChange={onChange} />);
    const input = open();
    fireEvent.change(input, { target: { value: "shopping-cart" } });
    const cell = document.querySelector('[role="gridcell"]');
    expect(cell).not.toBeNull();
    if (cell) fireEvent.click(cell);
    expect(onChange).toHaveBeenCalledWith("shopping-cart");
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("marks only the cell matching the current value as aria-selected, not the keyboard-highlighted one", () => {
    // value is "shopping-cart-plus" — alphabetically last among the three
    // matches, so it's never the index-0 cell opening resets the keyboard
    // highlight to. That keeps "highlighted" and "selected" genuinely
    // different cells, unlike matching on the query's own first result.
    render(<IconPicker value="shopping-cart-plus" onChange={vi.fn()} />);
    const input = open();
    fireEvent.change(input, { target: { value: "shopping-cart" } });
    const cells = Array.from(document.querySelectorAll('[role="gridcell"]'));
    const selected = cells.find((c) => c.getAttribute("aria-label") === "shopping-cart-plus");
    expect(selected?.getAttribute("aria-selected")).toBe("true");
    const highlighted = cells.find((c) => c.getAttribute("aria-label") === "shopping-cart");
    expect(highlighted?.getAttribute("aria-selected")).toBe("false");
  });

  it("a labeled clear button unsets the value", () => {
    const onChange = vi.fn();
    render(<IconPicker value="shopping-cart" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear shopping-cart" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("no clear button when there's no selected value", () => {
    render(<IconPicker value="" onChange={vi.fn()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("closes on a click outside the input and the panel", () => {
    render(<IconPicker value="" onChange={vi.fn()} />);
    const input = open();
    expect(input.getAttribute("aria-expanded")).toBe("true");
    fireEvent.mouseDown(document.body);
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("portals the panel to document.body, escaping an overflow: hidden ancestor", () => {
    const { container } = render(
      <div style={{ overflow: "hidden", height: "10px" }}>
        <IconPicker value="" onChange={vi.fn()} />
      </div>,
    );
    open();
    const grid = screen.getByRole("grid");
    expect(container.contains(grid)).toBe(false);
    expect(document.body.contains(grid)).toBe(true);
  });

  it("disables the input when disabled", () => {
    render(<IconPicker value="" onChange={vi.fn()} disabled />);
    expect((screen.getByRole("combobox") as HTMLInputElement).disabled).toBe(true);
  });

  describe("keyboard grid navigation", () => {
    it("Enter selects the currently active cell when there's no exact match", () => {
      const onChange = vi.fn();
      render(<IconPicker value="" onChange={onChange} />);
      const input = open();
      fireEvent.change(input, { target: { value: "shopping-cart" } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(onChange).toHaveBeenCalledWith("shopping-cart");
    });

    it("Enter prefers an exact typed match over the reset-to-0 highlighted cell", () => {
      // "book-user" sorts alphabetically before "user" among substring
      // matches for "user" — without the exact-match check, Enter would
      // commit "book-user" (index 0 after every keystroke resets it).
      const onChange = vi.fn();
      render(<IconPicker value="" onChange={onChange} />);
      const input = open();
      fireEvent.change(input, { target: { value: "user" } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(onChange).toHaveBeenCalledWith("user");
    });

    it("Enter with a query matching no known icon commits the raw typed text, preserving a legacy/unknown name", () => {
      const onChange = vi.fn();
      render(<IconPicker value="" onChange={onChange} />);
      const input = open();
      fireEvent.change(input, { target: { value: "a-legacy-icon-name" } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(onChange).toHaveBeenCalledWith("a-legacy-icon-name");
    });

    it("ArrowRight moves aria-activedescendant to the next cell", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      expect(input.getAttribute("aria-activedescendant")).toMatch(/-cell-0$/);
      fireEvent.keyDown(input, { key: "ArrowRight" });
      expect(input.getAttribute("aria-activedescendant")).toMatch(/-cell-1$/);
    });

    it("ArrowLeft at the first cell clamps rather than going negative", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      const before = input.getAttribute("aria-activedescendant");
      fireEvent.keyDown(input, { key: "ArrowLeft" });
      expect(input.getAttribute("aria-activedescendant")).toBe(before);
    });

    it("ArrowDown moves the active cell down by one full row (7 columns)", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      fireEvent.keyDown(input, { key: "ArrowDown" });
      expect(input.getAttribute("aria-activedescendant")).toMatch(/-cell-7$/);
    });

    it("ArrowUp at the top row clamps rather than moving before the first cell", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      const before = input.getAttribute("aria-activedescendant");
      fireEvent.keyDown(input, { key: "ArrowUp" });
      expect(input.getAttribute("aria-activedescendant")).toBe(before);
    });

    it("End moves to the last cell of the current row", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      fireEvent.keyDown(input, { key: "End" });
      expect(input.getAttribute("aria-activedescendant")).toMatch(/-cell-6$/);
    });

    it("Home moves back to the first cell of the current row", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      fireEvent.keyDown(input, { key: "End" });
      fireEvent.keyDown(input, { key: "Home" });
      expect(input.getAttribute("aria-activedescendant")).toMatch(/-cell-0$/);
    });

    it("Escape closes the panel and clears the query", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = open();
      fireEvent.change(input, { target: { value: "cart" } });
      fireEvent.keyDown(input, { key: "Escape" });
      expect(input.getAttribute("aria-expanded")).toBe("false");
      expect((input as HTMLInputElement).value).toBe("");
    });

    it("ArrowDown while closed opens the panel", () => {
      render(<IconPicker value="" onChange={vi.fn()} />);
      const input = screen.getByRole("combobox");
      expect(input.getAttribute("aria-expanded")).toBe("false");
      fireEvent.keyDown(input, { key: "ArrowDown" });
      expect(input.getAttribute("aria-expanded")).toBe("true");
    });
  });
});
