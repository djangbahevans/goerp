import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RelationPicker } from "./relation-picker.js";

afterEach(cleanup);

const ROWS = [
  { id: "1", display_name: "Acme Corp" },
  { id: "2", display_name: "Acme Industries" },
];

function fakeClient(rows = ROWS) {
  return { get: vi.fn().mockResolvedValue({ data: rows }) };
}

function fakeRegistry(overrides: Record<string, unknown> = {}) {
  return {
    resolve: vi.fn().mockResolvedValue({
      listRoute: "GET /contacts",
      labelField: "display_name",
      searchParam: "q",
      ...overrides,
    }),
  };
}

describe("RelationPicker", () => {
  it("opens on focus and shows the first page of results for an empty query", async () => {
    const client = fakeClient();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={client}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(await screen.findByRole("option", { name: "Acme Corp" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Acme Industries" })).toBeTruthy();
  });

  it("queries with the debounced search text under the 'q' param", async () => {
    const client = fakeClient();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={client}
        registry={fakeRegistry()}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "Acme" } });
    await waitFor(() =>
      expect(client.get).toHaveBeenCalledWith(
        "/contacts",
        expect.objectContaining({ params: expect.objectContaining({ q: "Acme" }) }),
      ),
    );
  });

  it("single-select: commits the selected value and closes on click", async () => {
    const onChange = vi.fn();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={onChange}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: "Acme Corp" }));
    expect(onChange).toHaveBeenCalledWith({ id: "1", display: "Acme Corp" });
    expect(screen.getByRole("combobox").getAttribute("aria-expanded")).toBe("false");
  });

  it("single-select: shows the selected display text in the trigger when closed", () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={{ id: "1", display: "Acme Corp" }}
        onChange={vi.fn()}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("Acme Corp");
  });

  it("single-select: a labeled clear button unsets the value back to null", () => {
    const onChange = vi.fn();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={{ id: "1", display: "Acme Corp" }}
        onChange={onChange}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear Acme Corp" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("single-select: no clear button when there's no selected value", () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("multiple: adds to the selection and stays open for further searching", async () => {
    const onChange = vi.fn();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={[]}
        onChange={onChange}
        multiple
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: "Acme Corp" }));
    expect(onChange).toHaveBeenCalledWith([{ id: "1", display: "Acme Corp" }]);
    expect(screen.getByRole("combobox").getAttribute("aria-expanded")).toBe("true");
  });

  it("multiple: excludes already-selected rows from the result list", async () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={[{ id: "1", display: "Acme Corp" }]}
        onChange={vi.fn()}
        multiple
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(await screen.findByRole("option", { name: "Acme Industries" })).toBeTruthy();
    expect(screen.queryByRole("option", { name: "Acme Corp" })).toBeNull();
  });

  it("multiple: removes a pill via its labeled remove button", () => {
    const onChange = vi.fn();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={[{ id: "1", display: "Acme Corp" }]}
        onChange={onChange}
        multiple
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove Acme Corp" }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("multiple: Backspace on an empty query removes the last selected pill", () => {
    const onChange = vi.fn();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={[
          { id: "1", display: "Acme Corp" },
          { id: "2", display: "Acme Industries" },
        ]}
        onChange={onChange}
        multiple
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Backspace" });
    expect(onChange).toHaveBeenCalledWith([{ id: "1", display: "Acme Corp" }]);
  });

  it("shows a loading skeleton while the query is in flight", async () => {
    const client = { get: <T,>() => new Promise<T>(() => {}) }; // never resolves
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={client}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    // The listbox is portaled to document.body (escaping any overflow:
    // hidden ancestor), so it's outside this render's own container.
    await waitFor(() => expect(document.querySelector("[data-skeleton='lines']")).toBeTruthy());
  });

  it("shows a 'no results' row, announced via aria-live, when the query matches nothing", async () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={fakeClient([])}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    const empty = await screen.findByText('No results for ""');
    expect(empty.getAttribute("aria-live")).toBe("polite");
  });

  it("shows an EmptyState when the target resource is unregistered", async () => {
    const registry = { resolve: vi.fn().mockResolvedValue(undefined) };
    render(
      <RelationPicker
        resource="x.y"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={fakeClient()}
        registry={registry}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(await screen.findByText("Module not installed")).toBeTruthy();
  });

  it("resolves labelField from the registry when the prop is omitted", async () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        value={null}
        onChange={vi.fn()}
        client={fakeClient()}
        registry={fakeRegistry({ labelField: "display_name" })}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(await screen.findByRole("option", { name: "Acme Corp" })).toBeTruthy();
  });

  it("queries under the registry's searchParam, not a hardcoded 'q'", async () => {
    const client = fakeClient();
    render(
      <RelationPicker
        resource="contacts.contact"
        value={null}
        onChange={vi.fn()}
        client={client}
        registry={fakeRegistry({ labelField: "display_name", searchParam: "search" })}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "Acme" } });
    await waitFor(() =>
      expect(client.get).toHaveBeenCalledWith(
        "/contacts",
        expect.objectContaining({ params: expect.objectContaining({ search: "Acme" }) }),
      ),
    );
  });

  it("sends a multi-value resourceFilter entry as a comma-joined filter[key][in] param", async () => {
    const client = fakeClient();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        resourceFilter={{ type: ["person", "company"] }}
        value={null}
        onChange={vi.fn()}
        client={client}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    await waitFor(() =>
      expect(client.get).toHaveBeenCalledWith(
        "/contacts",
        expect.objectContaining({ params: expect.objectContaining({ "filter[type][in]": "person,company" }) }),
      ),
    );
  });

  it("omits an empty-array resourceFilter entry instead of sending a literal empty filter[key][in]", async () => {
    const client = fakeClient();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        resourceFilter={{ type: [] }}
        value={null}
        onChange={vi.fn()}
        client={client}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    await waitFor(() => expect(client.get).toHaveBeenCalled());
    const params = client.get.mock.calls[0]?.[1]?.params as Record<string, unknown>;
    expect(params).not.toHaveProperty("filter[type][in]");
  });

  it("creatable: offers a create row and calls onCreate when there's no exact match", async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn().mockResolvedValue({ id: "9", display: "New Co" });
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={onChange}
        creatable
        onCreate={onCreate}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    await screen.findByRole("option", { name: "Acme Corp" });
    fireEvent.change(input, { target: { value: "New Co" } });
    fireEvent.click(await screen.findByRole("option", { name: 'Create "New Co"' }));
    expect(onCreate).toHaveBeenCalledWith("New Co");
    await waitFor(() => expect(onChange).toHaveBeenCalledWith({ id: "9", display: "New Co" }));
  });

  it("does not offer a create action when the query exactly matches an existing row's label", async () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        creatable
        onCreate={vi.fn()}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    await screen.findByRole("option", { name: "Acme Corp" });
    fireEvent.change(input, { target: { value: "Acme Corp" } });
    await waitFor(() => expect(screen.queryByRole("option", { name: /Create/ })).toBeNull());
  });

  it("truncates a long display value with a title attribute on its pill", () => {
    const longName = "A".repeat(120);
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={[{ id: "1", display: longName }]}
        onChange={vi.fn()}
        multiple
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    const label = screen.getByTitle(longName);
    expect(label.className).toContain("truncate");
  });

  it("ignores Enter/Backspace and disables option interaction while disabled", async () => {
    const onChange = vi.fn();
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={[{ id: "1", display: "Acme Corp" }]}
        onChange={onChange}
        multiple
        disabled
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "Backspace" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("closes on a click outside the input and the dropdown", async () => {
    render(
      <RelationPicker
        resource="contacts.contact"
        labelField="display_name"
        value={null}
        onChange={vi.fn()}
        client={fakeClient()}
        registry={fakeRegistry()}
      />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(await screen.findByRole("listbox")).toBeTruthy();

    fireEvent.mouseDown(document.body);
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
  });

  it("portals the dropdown to document.body, escaping an overflow: hidden ancestor", async () => {
    const { container } = render(
      <div style={{ overflow: "hidden", height: "10px" }}>
        <RelationPicker
          resource="contacts.contact"
          labelField="display_name"
          value={null}
          onChange={vi.fn()}
          client={fakeClient()}
          registry={fakeRegistry()}
        />
      </div>,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    const listbox = await screen.findByRole("listbox");
    expect(container.contains(listbox)).toBe(false);
    expect(document.body.contains(listbox)).toBe(true);
  });
});
