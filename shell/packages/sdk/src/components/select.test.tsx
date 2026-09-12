import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Select } from "./select.js";

afterEach(cleanup);

// jsdom doesn't implement scrollIntoView (jsdom/jsdom#1695) — Radix
// Select's own highlight-into-view effect calls it whenever the panel
// opens.
Element.prototype.scrollIntoView = vi.fn();

const OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "confirmed", label: "Confirmed", color: "green" },
  { value: "cancelled", label: "Cancelled", disabled: true },
];

describe("Select", () => {
  describe("single-select", () => {
    it("shows the placeholder when nothing is selected", () => {
      render(<Select options={OPTIONS} value="" onChange={vi.fn()} placeholder="Pick a status" />);
      expect(screen.getByRole("combobox").textContent).toContain("Pick a status");
    });

    it("shows the selected option's label in the trigger when closed", () => {
      render(<Select options={OPTIONS} value="draft" onChange={vi.fn()} />);
      expect(screen.getByRole("combobox").textContent).toContain("Draft");
    });

    it("opens the listbox and lists every option on trigger click", async () => {
      render(<Select options={OPTIONS} value="" onChange={vi.fn()} />);
      fireEvent.click(screen.getByRole("combobox"));
      expect(await screen.findByRole("option", { name: "Draft" })).toBeTruthy();
      expect(screen.getByRole("option", { name: "Confirmed" })).toBeTruthy();
      expect(screen.getByRole("option", { name: "Cancelled" })).toBeTruthy();
    });

    it("commits the picked value and closes", async () => {
      const onChange = vi.fn();
      render(<Select options={OPTIONS} value="" onChange={onChange} />);
      fireEvent.click(screen.getByRole("combobox"));
      fireEvent.click(await screen.findByRole("option", { name: "Draft" }));
      expect(onChange).toHaveBeenCalledWith("draft");
    });

    it("marks a disabled option aria-disabled without removing it from the list", async () => {
      render(<Select options={OPTIONS} value="" onChange={vi.fn()} />);
      fireEvent.click(screen.getByRole("combobox"));
      const option = await screen.findByRole("option", { name: "Cancelled" });
      expect(option.getAttribute("aria-disabled")).toBe("true");
    });

    it("a labeled clear button unsets the value back to empty", () => {
      const onChange = vi.fn();
      render(<Select options={OPTIONS} value="draft" onChange={onChange} />);
      fireEvent.click(screen.getByRole("button", { name: "Clear Draft" }));
      expect(onChange).toHaveBeenCalledWith("");
    });

    it("no clear button when there's no selected value", () => {
      render(<Select options={OPTIONS} value="" onChange={vi.fn()} />);
      expect(screen.queryByRole("button")).toBeNull();
    });

    it("emptyValue treats a real, matching option as unset — no clear button, no extra trigger padding", () => {
      const anyOption = { value: "any", label: "Any" };
      render(<Select options={[anyOption, ...OPTIONS]} value="any" emptyValue="any" onChange={vi.fn()} />);
      expect(screen.queryByRole("button")).toBeNull();
      expect(screen.getByRole("combobox").textContent).toContain("Any");
    });

    it("emptyValue: a clear button appears for any other selected value, and clearing reports emptyValue", () => {
      const anyOption = { value: "any", label: "Any" };
      const onChange = vi.fn();
      render(<Select options={[anyOption, ...OPTIONS]} value="draft" emptyValue="any" onChange={onChange} />);
      fireEvent.click(screen.getByRole("button", { name: "Clear Draft" }));
      expect(onChange).toHaveBeenCalledWith("any");
    });
  });

  describe("multi-select", () => {
    it("shows the placeholder when nothing is selected", () => {
      render(<Select options={OPTIONS} value={[]} onChange={vi.fn()} multiple placeholder="Pick statuses" />);
      expect(screen.getByRole("combobox").textContent).toContain("Pick statuses");
    });

    it("shows up to 2 selected labels in the closed trigger", () => {
      render(<Select options={OPTIONS} value={["draft", "confirmed"]} onChange={vi.fn()} multiple />);
      expect(screen.getByRole("combobox").textContent).toContain("Draft, Confirmed");
    });

    it("collapses more than 2 selections to a '+N more' suffix", () => {
      const options = [...OPTIONS, { value: "done", label: "Done" }];
      render(<Select options={options} value={["draft", "confirmed", "done"]} onChange={vi.fn()} multiple />);
      expect(screen.getByRole("combobox").textContent).toContain("Draft, Confirmed +1 more");
    });

    it("opens on click and stays open after toggling a selection", async () => {
      const onChange = vi.fn();
      render(<Select options={OPTIONS} value={[]} onChange={onChange} multiple />);
      const trigger = screen.getByRole("combobox");
      fireEvent.click(trigger);
      fireEvent.click(await screen.findByRole("option", { name: "Draft" }));
      expect(onChange).toHaveBeenCalledWith(["draft"]);
      expect(trigger.getAttribute("aria-expanded")).toBe("true");
    });

    it("toggling an already-selected option removes it", async () => {
      const onChange = vi.fn();
      render(<Select options={OPTIONS} value={["draft"]} onChange={onChange} multiple />);
      fireEvent.click(screen.getByRole("combobox"));
      fireEvent.click(await screen.findByRole("option", { name: "Draft" }));
      expect(onChange).toHaveBeenCalledWith([]);
    });

    it("ignores a click on a disabled option", async () => {
      const onChange = vi.fn();
      render(<Select options={OPTIONS} value={[]} onChange={onChange} multiple />);
      fireEvent.click(screen.getByRole("combobox"));
      fireEvent.click(await screen.findByRole("option", { name: "Cancelled" }));
      expect(onChange).not.toHaveBeenCalled();
    });

    it("Escape closes the panel", async () => {
      render(<Select options={OPTIONS} value={[]} onChange={vi.fn()} multiple />);
      const trigger = screen.getByRole("combobox");
      fireEvent.click(trigger);
      await screen.findByRole("option", { name: "Draft" });
      fireEvent.keyDown(trigger, { key: "Escape" });
      expect(trigger.getAttribute("aria-expanded")).toBe("false");
    });

    it("disabling the whole control disables the trigger button", () => {
      render(<Select options={OPTIONS} value={[]} onChange={vi.fn()} multiple disabled />);
      expect((screen.getByRole("combobox") as HTMLButtonElement).disabled).toBe(true);
    });

    it("closes on a click outside the trigger and the dropdown", async () => {
      render(<Select options={OPTIONS} value={[]} onChange={vi.fn()} multiple />);
      fireEvent.click(screen.getByRole("combobox"));
      expect(await screen.findByRole("listbox")).toBeTruthy();

      fireEvent.mouseDown(document.body);
      expect(screen.queryByRole("listbox")).toBeNull();
    });

    it("portals the dropdown to document.body, escaping an overflow: hidden ancestor", async () => {
      const { container } = render(
        <div style={{ overflow: "hidden", height: "10px" }}>
          <Select options={OPTIONS} value={[]} onChange={vi.fn()} multiple />
        </div>,
      );
      fireEvent.click(screen.getByRole("combobox"));
      const listbox = await screen.findByRole("listbox");
      expect(container.contains(listbox)).toBe(false);
      expect(document.body.contains(listbox)).toBe(true);
    });
  });
});
