import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TagsField } from "./tags-field.js";

afterEach(cleanup);

const options = [
  { id: "1", name: "VIP" },
  { id: "2", name: "Lead" },
];

describe("TagsField", () => {
  it("adds a matching option to the selection when clicked", () => {
    const onChange = vi.fn();
    render(<TagsField value={[]} onChange={onChange} options={options} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "VIP" } });
    fireEvent.click(screen.getByRole("option", { name: "VIP" }));
    expect(onChange).toHaveBeenCalledWith([{ id: "1", name: "VIP" }]);
  });

  it("removes a selected tag", () => {
    const onChange = vi.fn();
    render(<TagsField value={[{ id: "1", name: "VIP" }]} onChange={onChange} options={options} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove tag: VIP" }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("does not offer a create action when creatable is false", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "New Tag" } });
    expect(screen.queryByRole("option", { name: /Create/ })).toBeNull();
  });

  it("calls onCreate and adds the returned tag when creatable and no exact match exists", async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn().mockResolvedValue({ id: "3", name: "New Tag" });
    render(<TagsField value={[]} onChange={onChange} options={options} creatable onCreate={onCreate} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "New Tag" } });
    fireEvent.click(await screen.findByRole("option", { name: 'Create "New Tag"' }));
    expect(onCreate).toHaveBeenCalledWith("New Tag");
    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith([{ id: "3", name: "New Tag" }]));
  });

  it("does not offer a create action when the query exactly matches an existing option", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} creatable onCreate={vi.fn()} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "VIP" } });
    expect(screen.queryByRole("option", { name: /Create/ })).toBeNull();
  });

  it("uses the placeholder override instead of the default 'Add {label}…' text", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} placeholder="Add Skills…" />);
    expect(screen.getByPlaceholderText("Add Skills…")).toBeTruthy();
  });

  it("carries ARIA combobox attributes on the input, only expanded while suggestions are open", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} />);
    const input = screen.getByRole("combobox");
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect(input.getAttribute("aria-controls")).toBeTruthy();

    fireEvent.change(input, { target: { value: "VIP" } });
    expect(input.getAttribute("aria-expanded")).toBe("true");
    const listbox = document.getElementById(input.getAttribute("aria-controls") ?? "");
    expect(listbox?.getAttribute("role")).toBe("listbox");
  });

  it("moves a roving aria-activedescendant highlight with ArrowDown/ArrowUp", () => {
    const overlapping = [
      { id: "1", name: "Apple" },
      { id: "2", name: "Apricot" },
    ];
    render(<TagsField value={[]} onChange={vi.fn()} options={overlapping} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "ap" } });
    const appleOption = screen.getByRole("option", { name: "Apple" });
    const apricotOption = screen.getByRole("option", { name: "Apricot" });
    expect(input.getAttribute("aria-activedescendant")).toBe(appleOption.id);

    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(input.getAttribute("aria-activedescendant")).toBe(apricotOption.id);

    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(input.getAttribute("aria-activedescendant")).toBe(appleOption.id); // wraps

    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(input.getAttribute("aria-activedescendant")).toBe(apricotOption.id);
  });

  it("selects the highlighted suggestion on Enter", () => {
    const overlapping = [
      { id: "1", name: "Apple" },
      { id: "2", name: "Apricot" },
    ];
    const onChange = vi.fn();
    render(<TagsField value={[]} onChange={onChange} options={overlapping} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "ap" } });
    fireEvent.keyDown(input, { key: "ArrowDown" }); // Apple -> Apricot
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith([{ id: "2", name: "Apricot" }]);
  });

  it("closes the suggestion list on Escape without clearing the input", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "VIP" } });
    expect(input.getAttribute("aria-expanded")).toBe("true");

    fireEvent.keyDown(input, { key: "Escape" });
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect((input as HTMLInputElement).value).toBe("VIP");
  });

  it("closes the suggestion list on a click outside the field, same as Escape — without clearing the input", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "VIP" } });
    expect(input.getAttribute("aria-expanded")).toBe("true");

    fireEvent.mouseDown(document.body);
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect((input as HTMLInputElement).value).toBe("VIP");
  });

  it("reopens the suggestion list on ArrowDown after Escape dismissed it, without editing the query", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "VIP" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(input.getAttribute("aria-expanded")).toBe("false");

    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(input.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("option", { name: "VIP" })).toBeTruthy();
  });

  it("ignores Enter/Backspace/arrow keys while the field is disabled", () => {
    const onChange = vi.fn();
    render(<TagsField value={[{ id: "1", name: "VIP" }]} onChange={onChange} options={options} disabled />);
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "Backspace" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not move the roving highlight on hover while the field is disabled", () => {
    const overlapping = [
      { id: "1", name: "Apple" },
      { id: "2", name: "Apricot" },
    ];
    render(<TagsField value={[]} onChange={vi.fn()} options={overlapping} disabled />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "ap" } });
    const apricotOption = screen.getByRole("option", { name: "Apricot" });

    fireEvent.mouseEnter(apricotOption);
    expect(apricotOption.getAttribute("aria-selected")).toBe("false");
    expect(apricotOption.getAttribute("aria-disabled")).toBe("true");
  });

  it("removes the last selected tag on Backspace when the input is empty", () => {
    const onChange = vi.fn();
    render(
      <TagsField
        value={[
          { id: "1", name: "VIP" },
          { id: "2", name: "Lead" },
        ]}
        onChange={onChange}
        options={options}
      />,
    );
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Backspace" });
    expect(onChange).toHaveBeenCalledWith([{ id: "1", name: "VIP" }]);
  });

  it("does not remove a tag on Backspace when the input has text", () => {
    const onChange = vi.fn();
    render(<TagsField value={[{ id: "1", name: "VIP" }]} onChange={onChange} options={options} />);
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "x" } });
    fireEvent.keyDown(input, { key: "Backspace" });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("picks a readable text color against a tag's own arbitrary background color", () => {
    render(
      <TagsField
        value={[
          { id: "1", name: "Dark", color: "#1a1a2e" },
          { id: "2", name: "Light", color: "#f5f5f5" },
        ]}
        onChange={vi.fn()}
        options={options}
      />,
    );
    expect(screen.getByText("Dark").className).toContain("text-white");
    expect(screen.getByText("Light").className).toContain("text-black");
  });
});
