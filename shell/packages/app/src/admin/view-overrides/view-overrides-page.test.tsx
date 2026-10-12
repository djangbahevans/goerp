import { apiClient } from "@goerp/sdk";
import type { MetaSchema } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, type MockInstance, vi } from "vitest";
import { editableViews, elementGroups } from "./view-elements.js";
import { resetOverrides, saveElement, type ViewOverride } from "./view-overrides-api.js";
import { ViewOverridesPage } from "./view-overrides-page.js";

const SCHEMA = {
  engine_version: "dev",
  schema_hash: "base",
  modules: {
    crm: {
      name: "crm",
      views: [
        {
          name: "contacts_list",
          type: "list",
          resource: "crm.contact",
          columns: [{ field: "name", label: "Name" }, { field: "email" }],
        },
        {
          name: "contacts_form",
          type: "form",
          resource: "crm.contact",
          sections: [
            {
              name: "main",
              label: "Contact",
              fields: [
                { field: "name", label: "Full name", help_text: "Given and family name" },
                { field: "email", placeholder: "name@example.test" },
              ],
            },
          ],
        },
      ],
      models: { "crm.contact": { fields: [{ name: "name", required: true }] } },
    },
  },
} as unknown as MetaSchema;

let saved: ViewOverride[];
let post: MockInstance<typeof apiClient.post>;
let remove: MockInstance<typeof apiClient.delete>;

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
  saved = [];
  vi.spyOn(apiClient, "get").mockImplementation(async (path) => {
    if (path === "/_meta/schema") {
      const schema = structuredClone(SCHEMA);
      for (const view of schema.modules.crm?.views ?? []) {
        const declaration = view as Record<string, unknown>;
        const fields = declaration.columns ?? (declaration.sections as { fields: unknown[] }[])[0]?.fields;
        for (const field of fields as Record<string, unknown>[]) {
          for (const row of saved.filter((row) => row.view_name === declaration.name && row.element === field.field))
            field[row.property] = row.value;
        }
        if ((fields as Record<string, unknown>[]).some((field) => field.order !== undefined)) {
          (fields as Record<string, unknown>[]).sort((a, b) => Number(a.order ?? 0) - Number(b.order ?? 0));
        }
      }
      return schema as never;
    }
    return { data: saved } as never;
  });
  post = vi.spyOn(apiClient, "post").mockImplementation(async (_path, body) => {
    const row = body as ViewOverride;
    saved = saved.filter((item) => item.element !== row.element || item.property !== row.property);
    saved.push({ ...row, id: `${row.element}-${row.property}` });
    return row as never;
  });
  remove = vi.spyOn(apiClient, "delete").mockImplementation(async (path) => {
    saved = saved.filter((row) => !path.endsWith(`/${row.id}`));
    return undefined as never;
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

async function openView(name = "contacts_form") {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ViewOverridesPage />
    </QueryClientProvider>,
  );
  const picker = screen.getByRole("combobox");
  await waitFor(() => expect(picker).toHaveProperty("disabled", false));
  fireEvent.focus(picker);
  fireEvent.change(picker, { target: { value: name } });
  fireEvent.click(await screen.findByRole("option", { name: `${name} (${name.endsWith("form") ? "form" : "list"})` }));
  return screen.findByRole("group", { name: "name" });
}

describe("view overrides", () => {
  it("lists views with no overrides and shows their effective field defaults", async () => {
    const row = await openView();
    expect(within(row).getByLabelText("Label")).toHaveProperty("value", "Full name");
    expect(within(row).getByLabelText("Help text")).toHaveProperty("value", "Given and family name");
    expect(within(row).getByRole("checkbox", { name: "Required" })).toHaveProperty("checked", true);
    expect(
      screen.getAllByText("Hiding a field does not restrict API access. Only the field's access declaration does."),
    ).toHaveLength(2);
    expect(apiClient.get).toHaveBeenCalledWith(
      "/_meta/view-overrides",
      expect.objectContaining({ params: { view_name: "contacts_form" } }),
    );
  });

  it("saves edits and badges a customised row, then resets that row", async () => {
    const row = await openView();
    fireEvent.change(within(row).getByLabelText("Label"), { target: { value: "Customer" } });
    fireEvent.change(within(row).getByLabelText("Help text"), { target: { value: "Customer name" } });
    fireEvent.click(within(row).getByRole("button", { name: "Save field" }));
    await within(row).findByText("Customised");
    await waitFor(() =>
      expect(within(row).getByRole("button", { name: "Reset to default" })).toHaveProperty("disabled", false),
    );
    expect(post.mock.calls.map((call) => call[1])).toEqual([
      { view_name: "contacts_form", element: "name", property: "label", value: "Customer" },
      { view_name: "contacts_form", element: "name", property: "help_text", value: "Customer name" },
    ]);
    fireEvent.click(within(row).getByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(within(row).getByLabelText("Label")).toHaveProperty("value", "Full name"));
    expect(saved).toEqual([]);
  });

  it("prefills from saved overrides when the schema snapshot predates them", async () => {
    saved = [{ id: "label", view_name: "contacts_form", element: "name", property: "label", value: "Customer" }];
    vi.mocked(apiClient.get).mockImplementation(
      async (path) => (path === "/_meta/schema" ? structuredClone(SCHEMA) : { data: saved }) as never,
    );
    const row = await openView();
    expect(within(row).getByLabelText("Label")).toHaveProperty("value", "Customer");
    expect(within(row).getByText("Customised")).toBeTruthy();
  });

  it("keeps a rejected hidden-required draft and shows the server message on its row", async () => {
    post.mockRejectedValueOnce(new Error('field "name" cannot be hidden while required'));
    const row = await openView();
    fireEvent.click(within(row).getByRole("checkbox", { name: "Hidden" }));
    fireEvent.click(within(row).getByRole("button", { name: "Save field" }));
    expect(await within(row).findByRole("alert")).toHaveProperty(
      "textContent",
      'field "name" cannot be hidden while required',
    );
    expect(within(row).getByRole("checkbox", { name: "Hidden" })).toHaveProperty("checked", true);
    await waitFor(() => expect(row).toHaveProperty("disabled", false));
    fireEvent.click(within(row).getByRole("checkbox", { name: "Required" }));
    fireEvent.click(within(row).getByRole("button", { name: "Save field" }));
    await waitFor(() =>
      expect(post.mock.calls.at(-1)?.[1]).toEqual({
        view_name: "contacts_form",
        element: "name",
        property: "hidden",
        value: true,
        required: false,
      }),
    );
  });

  it("offers list columns without required controls and saves keyboard button reordering", async () => {
    await openView("contacts_list");
    expect(screen.queryByRole("checkbox", { name: "Required" })).toBeNull();
    const move = screen.getByRole("button", { name: "Move email up" });
    move.focus();
    fireEvent.click(move);
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    expect(post.mock.calls.map((call) => call[1])).toEqual([
      { view_name: "contacts_list", element: "email", property: "order", value: 0 },
      { view_name: "contacts_list", element: "name", property: "order", value: 1 },
    ]);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Reorder email" })));
  });

  it("reloads persisted properties after a partial save and retains the rejected draft", async () => {
    post
      .mockImplementationOnce(async (_path, body) => {
        saved.push({ ...(body as ViewOverride), id: "saved-label" });
        return undefined as never;
      })
      .mockRejectedValueOnce(new Error("Help text save failed"));
    const row = await openView();
    fireEvent.change(within(row).getByLabelText("Label"), { target: { value: "Customer" } });
    fireEvent.change(within(row).getByLabelText("Help text"), { target: { value: "Draft help" } });
    fireEvent.click(within(row).getByRole("button", { name: "Save field" }));
    await within(row).findByRole("alert");
    await within(row).findByText("Customised");
    await waitFor(() => expect(row).toHaveProperty("disabled", false));
    expect(within(row).getByLabelText("Help text")).toHaveProperty("value", "Draft help");
    expect(screen.getByRole("combobox")).toHaveProperty("disabled", true);
    fireEvent.click(within(row).getByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(within(row).getByLabelText("Label")).toHaveProperty("value", "Full name"));
    expect(remove).toHaveBeenCalledWith("/_meta/view-overrides/saved-label");
    expect(screen.getByRole("combobox")).toHaveProperty("disabled", false);
  });

  it("resets every override including removed elements, deleting hidden before required", async () => {
    saved = [
      { id: "required", view_name: "contacts_form", element: "name", property: "required", value: false },
      { id: "hidden", view_name: "contacts_form", element: "name", property: "hidden", value: true },
      { id: "removed", view_name: "contacts_form", element: "old", property: "label", value: "Old" },
    ];
    await openView();
    fireEvent.click(screen.getByRole("button", { name: "Reset all" }));
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(3));
    expect(remove.mock.calls.map((call) => call[0])).toEqual([
      "/_meta/view-overrides/hidden",
      "/_meta/view-overrides/required",
      "/_meta/view-overrides/removed",
    ]);
  });

  it("discovers nested tab fields and sub-list columns with related model defaults", () => {
    const schema = structuredClone(SCHEMA);
    const view = editableViews(schema).find((view) => view.type === "form");
    if (!view) throw new Error("Missing fixture view");
    view.declaration.tabs = [
      {
        label: "Details",
        sections: [{ name: "lines", type: "sub_list", field: "lines", columns: [{ field: "quantity" }] }],
      },
    ];
    schema.modules.crm!.models["crm.contact"]!.fields.push({
      name: "lines",
      type: "one2many",
      related_model: "crm.line",
    });
    schema.modules.crm!.models["crm.line"] = { fields: [{ name: "quantity", required: true }] } as never;
    const row = elementGroups(view, schema)
      .flatMap((group) => group.rows)
      .find((row) => row.element === "quantity");
    expect(row?.values.required).toBe(true);
    expect(row?.requiredControl).toBe(false);
  });

  it("clears hidden before changing required back to true", async () => {
    await saveElement("contacts_form", "name", { hidden: false, required: true });
    expect(post.mock.calls.map((call) => (call[1] as ViewOverride).property)).toEqual(["hidden", "required"]);
    await resetOverrides([]);
  });
});
