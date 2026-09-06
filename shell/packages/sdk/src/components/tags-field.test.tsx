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
    fireEvent.click(screen.getByRole("button", { name: "VIP" }));
    expect(onChange).toHaveBeenCalledWith([{ id: "1", name: "VIP" }]);
  });

  it("removes a selected tag", () => {
    const onChange = vi.fn();
    render(<TagsField value={[{ id: "1", name: "VIP" }]} onChange={onChange} options={options} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove VIP" }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("does not offer a create action when creatable is false", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "New Tag" } });
    expect(screen.queryByRole("button", { name: /Create/ })).toBeNull();
  });

  it("calls onCreate and adds the returned tag when creatable and no exact match exists", async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn().mockResolvedValue({ id: "3", name: "New Tag" });
    render(<TagsField value={[]} onChange={onChange} options={options} creatable onCreate={onCreate} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "New Tag" } });
    fireEvent.click(await screen.findByRole("button", { name: 'Create "New Tag"' }));
    expect(onCreate).toHaveBeenCalledWith("New Tag");
    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith([{ id: "3", name: "New Tag" }]));
  });

  it("does not offer a create action when the query exactly matches an existing option", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} creatable onCreate={vi.fn()} />);
    fireEvent.change(screen.getByPlaceholderText("Add a tag…"), { target: { value: "VIP" } });
    expect(screen.queryByRole("button", { name: /Create/ })).toBeNull();
  });

  it("uses the placeholder override instead of the default 'Add {label}…' text", () => {
    render(<TagsField value={[]} onChange={vi.fn()} options={options} placeholder="Add Skills…" />);
    expect(screen.getByPlaceholderText("Add Skills…")).toBeTruthy();
  });
});
