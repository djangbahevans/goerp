import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { type ReactNode, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Combobox, type ComboboxProps } from "./combobox.js";
import { FieldWrapper } from "./field-wrapper.js";

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(cleanup);

const fruits = ["Apple", "Banana", "Cherry"];

type HarnessProps = Partial<Omit<ComboboxProps<string>, "query" | "onQueryChange">> & {
  onQuery?: (query: string) => void;
};

function Harness({ options, onSelect = vi.fn(), onQuery, ...rest }: HarnessProps): ReactNode {
  const [query, setQuery] = useState("");
  const shown = options ?? fruits.filter((f) => f.toLowerCase().includes(query.toLowerCase()));
  return (
    <>
      <Combobox<string>
        query={query}
        onQueryChange={(next) => {
          setQuery(next);
          onQuery?.(next);
        }}
        options={shown}
        getOptionKey={(f) => f}
        getOptionLabel={(f) => f}
        onSelect={onSelect}
        placeholder="Pick a fruit"
        {...rest}
      />
      <button type="button">outside</button>
    </>
  );
}

function input(): HTMLInputElement {
  return screen.getByRole("combobox") as HTMLInputElement;
}

describe("Combobox", () => {
  it("opens on focus with the first option highlighted", () => {
    render(<Harness />);
    fireEvent.focus(input());
    expect(input().getAttribute("aria-expanded")).toBe("true");
    expect(screen.getAllByRole("option")).toHaveLength(3);
    expect(document.getElementById(input().getAttribute("aria-activedescendant") ?? "")?.textContent).toBe("Apple");
  });

  it("single-select: a pick closes the panel, clears the query and shows the selected label", () => {
    function Single() {
      const [value, setValue] = useState<string | undefined>();
      return <Harness selectedLabel={value} onSelect={setValue} />;
    }
    render(<Single />);
    fireEvent.change(input(), { target: { value: "ban" } });
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(input().getAttribute("aria-expanded")).toBe("false");
    expect(input().value).toBe("Banana");
  });

  it("reopens on a click in the already-focused input after a pick", () => {
    render(<Harness />);
    fireEvent.focus(input());
    fireEvent.click(screen.getByText("Apple"));
    expect(input().getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(input());
    expect(input().getAttribute("aria-expanded")).toBe("true");
  });

  it("closeOnSelect false: stays open, clears the query and resets the highlight", () => {
    const onSelect = vi.fn();
    render(<Harness closeOnSelect={false} onSelect={onSelect} />);
    fireEvent.change(input(), { target: { value: "an" } });
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(onSelect).toHaveBeenCalledWith("Banana");
    expect(input().getAttribute("aria-expanded")).toBe("true");
    expect(input().value).toBe("");
  });

  it("Tab dismisses without selecting and without preventing focus from moving", () => {
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    fireEvent.change(input(), { target: { value: "ch" } });
    const notPrevented = fireEvent.keyDown(input(), { key: "Tab" });
    expect(notPrevented).toBe(true);
    expect(input().getAttribute("aria-expanded")).toBe("false");
    expect(input().value).toBe("");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("keepQueryOnDismiss leaves the typed query after Escape", () => {
    render(<Harness keepQueryOnDismiss />);
    fireEvent.change(input(), { target: { value: "ch" } });
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(input().getAttribute("aria-expanded")).toBe("false");
    expect(input().value).toBe("ch");
  });

  it("closes on a pointer press outside the field and panel", () => {
    render(<Harness />);
    fireEvent.focus(input());
    fireEvent.mouseDown(screen.getByText("outside"));
    expect(input().getAttribute("aria-expanded")).toBe("false");
  });

  it("ArrowDown reopens a dismissed panel", () => {
    render(<Harness keepQueryOnDismiss />);
    fireEvent.change(input(), { target: { value: "a" } });
    fireEvent.keyDown(input(), { key: "Escape" });
    fireEvent.keyDown(input(), { key: "ArrowDown" });
    expect(input().getAttribute("aria-expanded")).toBe("true");
  });

  it("Home/End move the highlight on an empty query and the caret once text is typed", () => {
    render(<Harness />);
    fireEvent.focus(input());
    fireEvent.keyDown(input(), { key: "End" });
    expect(document.getElementById(input().getAttribute("aria-activedescendant") ?? "")?.textContent).toBe("Cherry");
    fireEvent.change(input(), { target: { value: "a" } });
    expect(fireEvent.keyDown(input(), { key: "End" })).toBe(true);
  });

  it("a caller's onKeyDown that prevents default takes the key", () => {
    const onSelect = vi.fn();
    render(
      <Harness
        onSelect={onSelect}
        onKeyDown={(event) => {
          if (event.key === "Enter") event.preventDefault();
        }}
      />,
    );
    fireEvent.focus(input());
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("shows a skeleton while loading with no options, and keeps options while loading more", () => {
    const { rerender } = render(<Harness options={[]} status="loading" />);
    fireEvent.focus(input());
    expect(document.querySelector("[data-skeleton='lines']")).toBeTruthy();
    rerender(<Harness options={["Apple"]} status="loading" />);
    expect(screen.getByRole("option").textContent).toBe("Apple");
    expect(screen.getByRole("listbox").getAttribute("aria-busy")).toBe("true");
  });

  it("shows the error content on error, with a default", () => {
    render(<Harness options={[]} status="error" />);
    fireEvent.focus(input());
    expect(screen.getByRole("listbox").textContent).toBe("Couldn't load results.");
  });

  it("announces result counts and the empty text through one persistent status region", async () => {
    render(<Harness />);
    const status = screen.getByRole("status");
    expect(status.textContent).toBe("");
    fireEvent.focus(input());
    await waitFor(() => expect(status.textContent).toBe("3 results"));
    fireEvent.change(input(), { target: { value: "ban" } });
    await waitFor(() => expect(status.textContent).toBe("1 result"));
    fireEvent.change(input(), { target: { value: "zzz" } });
    await waitFor(() => expect(status.textContent).toBe('No results for "zzz"'));
    fireEvent.keyDown(input(), { key: "Escape" });
    await waitFor(() => expect(status.textContent).toBe(""));
    expect(screen.getByRole("status")).toBe(status);
  });

  it("doesn't announce while loading", async () => {
    const { rerender } = render(<Harness options={["Apple"]} />);
    fireEvent.focus(input());
    const status = screen.getByRole("status");
    await waitFor(() => expect(status.textContent).toBe("1 result"));
    rerender(<Harness options={["Apple", "Banana"]} status="loading" />);
    expect(status.textContent).toBe("1 result");
  });

  it("the clear button and start adornment show only while closed with a selection", () => {
    const onClear = vi.fn();
    render(<Harness selectedLabel="Apple" onClear={onClear} startAdornment={<span data-testid="flag" />} />);
    expect(screen.getByTestId("flag")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear Apple" }));
    expect(onClear).toHaveBeenCalled();
    fireEvent.focus(input());
    expect(screen.queryByRole("button", { name: "Clear Apple" })).toBeNull();
    expect(screen.queryByTestId("flag")).toBeNull();
  });

  it("offers no clear button inside a required field", () => {
    render(
      <FieldWrapper label="Fruit" required>
        <Harness selectedLabel="Apple" onClear={vi.fn()} />
      </FieldWrapper>,
    );
    expect(screen.queryByRole("button", { name: "Clear Apple" })).toBeNull();
  });

  it("renders `above` inside the field, and presses on it don't dismiss", () => {
    render(<Harness above={<button type="button">pill</button>} />);
    fireEvent.focus(input());
    fireEvent.mouseDown(screen.getByText("pill"));
    expect(input().getAttribute("aria-expanded")).toBe("true");
  });

  it("names each option by getOptionLabel even when renderOption adds decoration", () => {
    render(<Harness renderOption={(f) => <span>🍎 {f}</span>} />);
    fireEvent.focus(input());
    expect(screen.getByRole("option", { name: "Apple" })).toBeTruthy();
  });

  it("reports open changes", () => {
    const onOpenChange = vi.fn();
    render(<Harness onOpenChange={onOpenChange} />);
    fireEvent.focus(input());
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
  });

  it("never opens while disabled", () => {
    render(<Harness disabled />);
    fireEvent.focus(input());
    fireEvent.keyDown(input(), { key: "ArrowDown" });
    expect(input().getAttribute("aria-expanded")).toBe("false");
  });

  it("an outside press clears the query typed after the panel opened", () => {
    render(<Harness />);
    fireEvent.focus(input());
    fireEvent.change(input(), { target: { value: "ban" } });
    fireEvent.mouseDown(screen.getByText("outside"));
    expect(input().getAttribute("aria-expanded")).toBe("false");
    expect(input().value).toBe("");
  });

  it("holds the query and panel while an async onSelect settles, and keeps them if it rejects", async () => {
    let reject: (reason: Error) => void = () => {};
    const onSelect = vi.fn(() => new Promise<void>((_, r) => (reject = r)));
    render(<Harness onSelect={onSelect} />);
    fireEvent.change(input(), { target: { value: "ch" } });
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(input().getAttribute("aria-expanded")).toBe("true");
    expect(input().value).toBe("ch");
    reject(new Error("create failed"));
    await waitFor(() => expect(onSelect).toHaveBeenCalled());
    expect(input().getAttribute("aria-expanded")).toBe("true");
    expect(input().value).toBe("ch");
  });

  it("closes once an async onSelect resolves", async () => {
    render(<Harness onSelect={async () => {}} />);
    fireEvent.change(input(), { target: { value: "ch" } });
    fireEvent.keyDown(input(), { key: "Enter" });
    await waitFor(() => expect(input().getAttribute("aria-expanded")).toBe("false"));
    expect(input().value).toBe("");
  });

  it("Enter with no options in an open panel doesn't submit a surrounding form", () => {
    render(<Harness options={[]} />);
    fireEvent.focus(input());
    expect(fireEvent.keyDown(input(), { key: "Enter" })).toBe(false);
  });

  it("an error replaces options kept from an earlier search", () => {
    render(<Harness options={["Apple"]} status="error" />);
    fireEvent.focus(input());
    expect(screen.queryByRole("option")).toBeNull();
    expect(screen.getByRole("listbox").textContent).toBe("Couldn't load results.");
    expect(input().getAttribute("aria-activedescendant")).toBeNull();
  });

  it("leaves rows that don't count as results out of the announced count", async () => {
    render(<Harness options={['Create "zzz"']} countsAsResult={(o) => !o.startsWith("Create")} />);
    fireEvent.focus(input());
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("No results"));
  });
});
