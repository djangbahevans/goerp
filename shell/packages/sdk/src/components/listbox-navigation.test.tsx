import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LISTBOX_PAGE_JUMP, type ListboxNavigationOptions, useListboxNavigation } from "./listbox-navigation.js";

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(cleanup);

function Harness({ items, ...options }: Omit<ListboxNavigationOptions, "count"> & { items: string[] }) {
  const nav = useListboxNavigation({ ...options, count: items.length });
  return (
    <>
      <input aria-label="search" {...nav.inputProps} onKeyDown={nav.handleKeyDown} />
      <div id={nav.listboxId} role="listbox">
        {items.map((item, index) => (
          <div key={item} {...nav.getOptionProps(index, { disabled: item === "disabled" })}>
            {item}
          </div>
        ))}
      </div>
    </>
  );
}

const letters = Array.from({ length: 25 }, (_, i) => String.fromCharCode(97 + i));

function activeText(): string | null | undefined {
  const id = screen.getByRole("combobox").getAttribute("aria-activedescendant");
  return id ? document.getElementById(id)?.textContent : null;
}

describe("useListboxNavigation", () => {
  it("wraps at either end by default", () => {
    render(<Harness items={["a", "b", "c"]} isOpen onChoose={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(activeText()).toBe("c");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(activeText()).toBe("a");
  });

  it("clamps at either end with wrap: false", () => {
    render(<Harness items={["a", "b", "c"]} isOpen wrap={false} onChoose={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(activeText()).toBe("a");
    fireEvent.keyDown(input, { key: "End" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(activeText()).toBe("c");
  });

  it("Home/End jump to the ends only when homeEnd is on", () => {
    const { rerender } = render(<Harness items={["a", "b", "c"]} isOpen onChoose={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "End" });
    expect(activeText()).toBe("c");
    rerender(<Harness items={["a", "b", "c"]} isOpen homeEnd={false} onChoose={vi.fn()} />);
    const home = fireEvent.keyDown(input, { key: "Home" });
    expect(home).toBe(true); // not prevented, so the caret moves
    expect(activeText()).toBe("c");
  });

  it("PageDown/PageUp jump a page and stop at the ends", () => {
    render(<Harness items={letters} isOpen onChoose={vi.fn()} />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "PageDown" });
    expect(activeText()).toBe(letters[LISTBOX_PAGE_JUMP]);
    fireEvent.keyDown(input, { key: "PageDown" });
    fireEvent.keyDown(input, { key: "PageDown" });
    expect(activeText()).toBe(letters.at(-1));
    fireEvent.keyDown(input, { key: "PageUp" });
    fireEvent.keyDown(input, { key: "PageUp" });
    fireEvent.keyDown(input, { key: "PageUp" });
    expect(activeText()).toBe("a");
  });

  it("Enter chooses the highlighted option", () => {
    const onChoose = vi.fn();
    render(<Harness items={["a", "b"]} isOpen onChoose={onChoose} />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChoose).toHaveBeenCalledWith(1);
  });

  it("leaves Enter alone with no options, so a surrounding form still submits", () => {
    const onChoose = vi.fn();
    render(<Harness items={[]} isOpen onChoose={onChoose} />);
    const notPrevented = fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter" });
    expect(notPrevented).toBe(true);
    expect(onChoose).not.toHaveBeenCalled();
  });

  it("handles nothing while closed and has no active descendant", () => {
    const onChoose = vi.fn();
    render(<Harness items={["a", "b"]} isOpen={false} onChoose={onChoose} />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChoose).not.toHaveBeenCalled();
    expect(input.getAttribute("aria-activedescendant")).toBeNull();
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("ignores keys during IME composition", () => {
    const onChoose = vi.fn();
    render(<Harness items={["a", "b"]} isOpen onChoose={onChoose} />);
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter", isComposing: true });
    expect(onChoose).not.toHaveBeenCalled();
  });

  it("hover highlights, click chooses, and a pointer press keeps focus in the input", () => {
    const onChoose = vi.fn();
    render(<Harness items={["a", "b"]} isOpen onChoose={onChoose} />);
    const option = screen.getByText("b");
    fireEvent.mouseEnter(option);
    expect(activeText()).toBe("b");
    expect(fireEvent.mouseDown(option)).toBe(false);
    fireEvent.click(option);
    expect(onChoose).toHaveBeenCalledWith(1);
  });

  it("a disabled option can't be highlighted by hover or chosen by click", () => {
    const onChoose = vi.fn();
    render(<Harness items={["a", "disabled"]} isOpen onChoose={onChoose} />);
    const option = screen.getByText("disabled");
    expect(option.getAttribute("aria-disabled")).toBe("true");
    fireEvent.mouseEnter(option);
    fireEvent.click(option);
    expect(activeText()).toBe("a");
    expect(onChoose).not.toHaveBeenCalled();
  });

  it("scrolls the highlighted option into view as it moves", () => {
    render(<Harness items={["a", "b"]} isOpen onChoose={vi.fn()} />);
    vi.mocked(Element.prototype.scrollIntoView).mockClear();
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "ArrowDown" });
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
  });
});
