import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { ModelDef, ResourceRegistryEntry } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ListColumn, Row } from "../list/list-view-types.js";
import { EditableSubList, type EditableSubListProps } from "./editable-sub-list.js";
import type { FormSection } from "./form-view-types.js";

const WRITE = "sales:order:write";

const entry: ResourceRegistryEntry = {
  module: "sales",
  resource: "sales.order_line",
  listPath: "/order-lines",
  getPath: "/order-lines/{id}",
  createPath: "/order-lines",
  updatePath: "/order-lines/{id}",
  deletePath: "/order-lines/{id}",
  pivotPath: null,
  listMethod: "GET",
  createMethod: "POST",
  updateMethod: "PATCH",
  deleteMethod: "DELETE",
  createPermissions: [WRITE],
  updatePermissions: [WRITE],
  deletePermissions: [WRITE],
  previewPath: null,
};

const model: ModelDef = {
  name: "sales.order_line",
  label: "Order line",
  label_plural: "Order lines",
  fields: [
    { name: "description", type: "text", required: true },
    { name: "qty", type: "integer" },
    { name: "subtotal", type: "decimal" },
  ],
  enabled_ops: ["list", "create", "update", "delete"],
  shareable: false,
};

const columns: ListColumn[] = [
  { field: "description", label: "Description", primary: true },
  { field: "qty", label: "Qty", type: "number" },
  { field: "subtotal", label: "Subtotal", type: "number", readonly: true },
];

const lines: Row[] = [
  { id: "l1", description: "Widget A", qty: 2, subtotal: 20 },
  { id: "l2", description: "Widget B", qty: 1, subtotal: 5 },
];

function fakeClient() {
  return {
    get: vi.fn(),
    post: vi.fn(async (_path: string, body?: object): Promise<Row> => ({ id: "l3", ...(body as Row) })),
    put: vi.fn(),
    patch: vi.fn(async (_path: string, body?: object) => ({ ...lines[0], ...(body as Row) })),
    delete: vi.fn(async () => undefined),
  };
}

type FakeClient = ReturnType<typeof fakeClient>;

// The mocks' methods aren't generic the way APIClient's are.
function asClient(client: FakeClient): NonNullable<EditableSubListProps["client"]> {
  return client as unknown as NonNullable<EditableSubListProps["client"]>;
}

afterEach(() => {
  cleanup();
});

function renderList(
  overrides: Omit<Partial<EditableSubListProps>, "client"> & {
    client?: FakeClient;
    section?: FormSection;
    granted?: string[];
    rows?: Row[];
  } = {},
) {
  const { granted = [WRITE], rows = lines, section, client: givenClient, ...props } = overrides;
  const client = givenClient ?? fakeClient();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const permissions = createPermissionContextValue({
    permissions: new Set(granted),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <PermissionContext.Provider value={permissions}>
        <EditableSubList
          section={section ?? { type: "sub_list", field: "line_ids", inline_key: "lines", inline_edit: true }}
          parentResource="sales.order"
          parentRecord={{ id: "o1", lines: rows }}
          recordId="o1"
          target={{ relatedModel: "sales.order_line", inverseField: "order_id" }}
          columns={columns}
          label="Order Lines"
          formReadonly={false}
          readOnlyFallback={<p>read-only fallback</p>}
          client={asClient(client)}
          resources={{ resolve: async () => entry }}
          models={{ resolve: async () => model }}
          actions={{ resolve: vi.fn() }}
          {...props}
        />
      </PermissionContext.Provider>
    </QueryClientProvider>,
  );
  return client;
}

async function openRow(name: string) {
  fireEvent.click(await screen.findByRole("button", { name: `Edit ${name}` }));
}

describe("EditableSubList", () => {
  it("opens a row in place, naming each control by column and row, and focuses the first", async () => {
    renderList();
    await openRow("Widget A");

    const description = await screen.findByLabelText("Description, Widget A");
    await waitFor(() => expect(document.activeElement).toBe(description));
    expect(screen.getByLabelText("Qty, Widget A")).toBeTruthy();
    // The readonly column keeps its view-mode rendering.
    expect(screen.queryByLabelText("Subtotal, Widget A")).toBeNull();
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
  });

  it("saves only the changed fields through the update route, then returns focus to Edit", async () => {
    const client = renderList();
    await openRow("Widget A");
    fireEvent.change(await screen.findByLabelText("Qty, Widget A"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(client.patch).toHaveBeenCalledWith("/order-lines/l1", { qty: 5 }));
    const edit = await screen.findByRole("button", { name: "Edit Widget A" });
    await waitFor(() => expect(document.activeElement).toBe(edit));
    expect(screen.getByRole("status").textContent).toContain("Order line saved.");
  });

  it("saves on Enter in a text input and discards on Escape", async () => {
    const client = renderList();
    await openRow("Widget A");
    const qty = await screen.findByLabelText("Qty, Widget A");
    fireEvent.change(qty, { target: { value: "9" } });
    fireEvent.keyDown(qty, { key: "Escape" });

    expect(screen.queryByLabelText("Qty, Widget A")).toBeNull();
    expect(client.patch).not.toHaveBeenCalled();

    await openRow("Widget A");
    const reopened = await screen.findByLabelText("Qty, Widget A");
    fireEvent.change(reopened, { target: { value: "3" } });
    fireEvent.keyDown(reopened, { key: "Enter" });
    await waitFor(() => expect(client.patch).toHaveBeenCalledWith("/order-lines/l1", { qty: 3 }));
  });

  it("adds a row, creating it with the inverse field, and returns focus to Add", async () => {
    const client = renderList();
    fireEvent.click(await screen.findByRole("button", { name: "Add Order line" }));
    const description = await screen.findByLabelText("Description");
    fireEvent.change(description, { target: { value: "Gadget" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(client.post).toHaveBeenCalledWith("/order-lines", { description: "Gadget", order_id: "o1" }),
    );
    expect(await screen.findByText("Gadget")).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Add Order line" })));
  });

  it("discards a new row on Cancel without a request", async () => {
    const client = renderList();
    fireEvent.click(await screen.findByRole("button", { name: "Add Order line" }));
    await screen.findByLabelText("Description");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByLabelText("Description")).toBeNull();
    expect(client.post).not.toHaveBeenCalled();
  });

  it("flags an empty required cell without sending anything", async () => {
    const client = renderList();
    fireEvent.click(await screen.findByRole("button", { name: "Add Order line" }));
    fireEvent.change(await screen.findByLabelText("Qty"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Required.")).toBeTruthy();
    const description = screen.getByLabelText("Description");
    expect(description.getAttribute("aria-invalid")).toBe("true");
    await waitFor(() => expect(document.activeElement).toBe(description));
    expect(client.post).not.toHaveBeenCalled();
  });

  it("shows a 422's messages in their cells and the rest on the row error line", async () => {
    const client = fakeClient();
    client.patch.mockRejectedValue(
      new AppError({
        code: "validation_failed",
        message: "some fields are invalid",
        httpStatus: 422,
        details: { qty: ["Must be positive"], tax_id: ["Unknown tax"] },
      }),
    );
    renderList({ client });
    await openRow("Widget A");
    fireEvent.change(await screen.findByLabelText("Qty, Widget A"), { target: { value: "-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Must be positive")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("tax_id: Unknown tax");
    // Still open, with the value the user typed.
    expect((screen.getByLabelText("Qty, Widget A") as HTMLInputElement).value).toBe("-1");
  });

  it("saves the open row before opening another, and stays put when that save fails", async () => {
    const client = fakeClient();
    client.patch.mockRejectedValueOnce(new Error("Network down"));
    renderList({ client });
    await openRow("Widget A");
    fireEvent.change(await screen.findByLabelText("Qty, Widget A"), { target: { value: "4" } });
    fireEvent.click(screen.getByRole("button", { name: "Edit Widget B" }));

    expect(await screen.findByText("Network down")).toBeTruthy();
    expect(screen.getByLabelText("Qty, Widget A")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Edit Widget B" }));
    expect(await screen.findByLabelText("Qty, Widget B")).toBeTruthy();
    expect(client.patch).toHaveBeenLastCalledWith("/order-lines/l1", { qty: 4 });
  });

  it("deletes a row after confirmation", async () => {
    const client = renderList();
    fireEvent.click(await screen.findByRole("button", { name: "Delete Widget B" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(client.delete).toHaveBeenCalledWith("/order-lines/l2"));
    await waitFor(() => expect(screen.queryByText("Widget B")).toBeNull());
  });

  it("merges preview values into untouched fields while editing", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const client = fakeClient();
      client.post.mockImplementation(async (path: string, body?: object) =>
        path === "/order-lines/preview" ? { ...(body as Row), subtotal: 70 } : { id: "l3" },
      );
      renderList({
        client,
        resources: { resolve: async () => ({ ...entry, previewPath: "/order-lines/preview" }) },
        models: { resolve: async () => ({ ...model, enabled_ops: [...model.enabled_ops, "preview"] }) },
      });
      await openRow("Widget A");
      fireEvent.change(await screen.findByLabelText("Qty, Widget A"), { target: { value: "7" } });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(350);
      });

      expect(client.post).toHaveBeenCalledWith("/order-lines/preview", expect.objectContaining({ id: "l1", qty: 7 }));
      expect(await screen.findByText("70")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("applies only the latest preview answer when an earlier one resolves after it", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const client = fakeClient();
      const answers: Array<(row: Row) => void> = [];
      client.post.mockImplementation(
        (path: string) =>
          new Promise<Row>((resolve) => {
            if (path === "/order-lines/preview") answers.push(resolve);
            else resolve({ id: "l3" });
          }),
      );
      renderList({
        client,
        resources: { resolve: async () => ({ ...entry, previewPath: "/order-lines/preview" }) },
        models: { resolve: async () => ({ ...model, enabled_ops: [...model.enabled_ops, "preview"] }) },
      });
      await openRow("Widget A");
      const qty = await screen.findByLabelText("Qty, Widget A");
      // Widget A's qty is already 2, so start from a different value.
      fireEvent.change(qty, { target: { value: "4" } });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(350);
      });
      expect(answers).toHaveLength(1);
      fireEvent.change(qty, { target: { value: "5" } });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(350);
      });
      expect(answers).toHaveLength(2);

      // The newer request answers first, then the stale one.
      await act(async () => {
        answers[1]?.({ id: "l1", qty: 5, subtotal: 555 });
        answers[0]?.({ id: "l1", qty: 4, subtotal: 444 });
      });
    } finally {
      vi.useRealTimers();
    }

    expect(screen.getByText("555")).toBeTruthy();
    expect(screen.queryByText("444")).toBeNull();
  });

  it("doesn't add a local row keyed by its client id when a create returns no body", async () => {
    const client = fakeClient();
    client.post.mockResolvedValue(undefined as unknown as Row);
    renderList({ client });
    fireEvent.click(await screen.findByRole("button", { name: "Add Order line" }));
    fireEvent.change(await screen.findByLabelText("Description"), { target: { value: "Gadget" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(client.post).toHaveBeenCalled());
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Add Order line" })));
    expect(screen.queryByRole("button", { name: /Edit Gadget/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Edit row/ })).toBeNull();
  });

  it("keeps a saved edit to a row created this session after the refetch it triggers", async () => {
    const client = fakeClient();
    let fetches = 0;
    // The created row never comes back in the loaded page (it sorts past
    // it), and each refetch is a fresh snapshot.
    client.get.mockImplementation(async () => {
      fetches += 1;
      return { data: lines, meta: { cursor: null, has_more: false, total: fetches } };
    });
    client.post.mockResolvedValue({ id: "l3", description: "Gadget", qty: 1 });
    client.patch.mockImplementation(async (_path: string, body?: object) => ({
      id: "l3",
      description: "Gadget",
      qty: 1,
      ...(body as Row),
    }));
    renderList({ client, section: { type: "sub_list", field: "line_ids", inline_edit: true } });

    fireEvent.click(await screen.findByRole("button", { name: "Add Order line" }));
    fireEvent.change(await screen.findByLabelText("Description"), { target: { value: "Gadget" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await openRow("Gadget");
    fireEvent.change(await screen.findByLabelText("Qty, Gadget"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(client.patch).toHaveBeenCalledWith("/order-lines/l3", { qty: 5 }));
    const fetchesAfterSave = fetches;
    await waitFor(() => expect(fetches).toBeGreaterThan(fetchesAfterSave - 1));
    await waitFor(() => {
      const row = screen.getByText("Gadget").closest("tr") as HTMLElement;
      expect(within(row).getByText("5")).toBeTruthy();
    });
    // Still there once the refetch has settled.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    const row = screen.getByText("Gadget").closest("tr") as HTMLElement;
    expect(within(row).getByText("5")).toBeTruthy();
  });

  it("disables Add once max_rows is reached", async () => {
    renderList({
      section: { type: "sub_list", field: "line_ids", inline_key: "lines", inline_edit: true, max_rows: 2 },
    });
    const add = await screen.findByRole("button", { name: "Add Order line" });
    expect((add as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Up to 2 order lines.")).toBeTruthy();
  });

  it("renders the read-only fallback without write access, or on a read-only form", async () => {
    renderList({ granted: [] });
    expect(await screen.findByText("read-only fallback")).toBeTruthy();
    cleanup();
    renderList({ formReadonly: true });
    expect(await screen.findByText("read-only fallback")).toBeTruthy();
  });

  it("shows an empty state whose action adds the first row", async () => {
    renderList({ rows: [] });
    expect(await screen.findByText("No order lines yet")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Add Order line" }));
    expect(await screen.findByLabelText("Description")).toBeTruthy();
  });

  it("fetches rows filtered by the inverse field when there is no inline_key", async () => {
    const client = fakeClient();
    client.get.mockResolvedValue({ data: lines, meta: { cursor: null, has_more: false } });
    renderList({
      client,
      section: { type: "sub_list", field: "line_ids", inline_edit: true, sort: "sequence ASC" },
    });
    expect(await screen.findByText("Widget A")).toBeTruthy();
    expect(client.get).toHaveBeenCalledWith("/order-lines", {
      params: { "filter[order_id]": "o1", sort: "sequence" },
    });
  });
});
