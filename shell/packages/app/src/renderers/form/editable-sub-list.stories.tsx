import { SectionCard } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import type { ModelDef, ResourceRegistryEntry } from "@goerp/sdk/schema";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import type { ListColumn, Row } from "../list/list-view-types.js";
import { EditableSubList, type EditableSubListProps } from "./editable-sub-list.js";

// docs/components/editable-sub-list.md's States table, against an
// in-memory order-lines backend: validation on save, and a preview route
// recomputing `subtotal` from qty × unit price as the row is edited.

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
  createPermissions: [],
  updatePermissions: [],
  deletePermissions: [],
  previewPath: "/order-lines/preview",
};

const model: ModelDef = {
  name: "sales.order_line",
  label: "Order line",
  label_plural: "Order lines",
  fields: [
    { name: "description", type: "text", required: true },
    { name: "qty", type: "integer", required: true },
    { name: "price_unit", type: "decimal" },
    { name: "taxable", type: "boolean" },
    { name: "subtotal", type: "decimal" },
  ],
  enabled_ops: ["list", "create", "update", "delete", "preview"],
  shareable: false,
};

const columns: ListColumn[] = [
  { field: "description", label: "Description", primary: true, min_width: 200 },
  { field: "qty", label: "Qty", type: "number", width: 96, align: "right" },
  { field: "price_unit", label: "Unit price", type: "currency", currency_field: "currency_code", width: 160 },
  { field: "taxable", label: "Taxable", type: "boolean", width: 88 },
  {
    field: "subtotal",
    label: "Subtotal",
    type: "currency",
    currency_field: "currency_code",
    readonly: true,
    width: 180,
    align: "right",
  },
];

const SEED: Row[] = [
  // Integer minor units (pesewas), l10n-guide.md's money convention.
  { id: "l1", description: "Office chair", qty: 4, price_unit: 85000, taxable: true, currency_code: "GHS" },
  { id: "l2", description: "Standing desk", qty: 2, price_unit: 240000, taxable: true, currency_code: "GHS" },
  { id: "l3", description: "Delivery", qty: 1, price_unit: 15000, taxable: false, currency_code: "GHS" },
].map(withSubtotal);

function withSubtotal(row: Row): Row {
  const qty = typeof row.qty === "number" ? row.qty : 0;
  const price = typeof row.price_unit === "number" ? row.price_unit : 0;
  return { ...row, subtotal: qty * price };
}

function validate(row: Row): void {
  const details: Record<string, string[]> = {};
  if (typeof row.description !== "string" || row.description.trim() === "") details.description = ["Required."];
  if (typeof row.qty !== "number" || row.qty <= 0) details.qty = ["Must be greater than zero."];
  if (Object.keys(details).length > 0) {
    throw new AppError({ code: "validation_failed", message: "some fields are invalid", httpStatus: 422, details });
  }
}

function fakeBackend(seed: Row[]) {
  let rows = seed.map((row) => ({ ...row }));
  let next = rows.length + 1;
  const idOf = (path: string) => path.split("/").pop() ?? "";
  const delay = () => new Promise((resolve) => setTimeout(resolve, 250));
  return {
    get: async <T,>() => ({ data: rows, meta: { cursor: null, has_more: false, total: rows.length } }) as T,
    post: async <T,>(path: string, body?: object) => {
      const draft = withSubtotal({ currency_code: "GHS", ...(body as Row) });
      if (path.endsWith("/preview")) return draft as T;
      await delay();
      validate(draft);
      const created = { ...draft, id: `l${next++}` };
      rows = [...rows, created];
      return created as T;
    },
    patch: async <T,>(path: string, body?: object) => {
      await delay();
      const current = rows.find((row) => row.id === idOf(path));
      const updated = withSubtotal({ ...current, ...(body as Row) });
      validate(updated);
      rows = rows.map((row) => (row.id === updated.id ? updated : row));
      return updated as T;
    },
    put: async <T,>() => ({}) as T,
    delete: async <T,>(path: string) => {
      rows = rows.filter((row) => row.id !== idOf(path));
      return undefined as T;
    },
  };
}

function Harness(props: Partial<EditableSubListProps> & { seed?: Row[] }) {
  const { seed = SEED, ...rest } = props;
  const [client] = useState(() => fakeBackend(seed));
  return (
    <SectionCard title="Order Lines">
      <EditableSubList
        section={{ type: "sub_list", field: "line_ids", inline_edit: true, add_label: "Add product" }}
        parentResource="sales.order"
        parentRecord={{ id: "o1" }}
        recordId="o1"
        target={{ relatedModel: "sales.order_line", inverseField: "order_id" }}
        columns={columns}
        label="Order Lines"
        formReadonly={false}
        readOnlyFallback={<p className="text-sm text-text-secondary">Read-only rendering (InlineSubList).</p>}
        client={client}
        resources={{ resolve: async () => entry }}
        models={{ resolve: async () => model }}
        actions={{ resolve: async () => ({ method: "POST", path: "/", permissions: [] }) }}
        {...rest}
      />
    </SectionCard>
  );
}

// Fresh per story, so one story's saved rows never leak into another.
const withQueryClient: Decorator = (Story) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <div style={{ maxWidth: 960 }}>
      <Story />
    </div>
  </QueryClientProvider>
);

const meta = {
  title: "Renderers/Form/EditableSubList",
  component: Harness,
  decorators: [withQueryClient],
} satisfies Meta<typeof Harness>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};

export const EditingRow: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Edit Office chair" }));
    const qty = await canvas.findByLabelText("Qty, Office chair");
    await userEvent.clear(qty);
    await userEvent.type(qty, "6");
    // Preview recomputes the read-only subtotal before save.
    await expect(await canvas.findByText(/5,100\.00/)).toBeTruthy();
  },
};

export const ValidationErrors: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Edit Standing desk" }));
    const qty = await canvas.findByLabelText("Qty, Standing desk");
    await userEvent.clear(qty);
    await userEvent.type(qty, "0");
    await userEvent.click(canvas.getByRole("button", { name: "Save" }));
    await expect(await canvas.findByText("Must be greater than zero.")).toBeTruthy();
  },
};

export const AddingRow: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Add product" }));
    await userEvent.type(await canvas.findByLabelText("Description"), "Monitor arm");
  },
};

export const RowError: Story = {
  args: {
    client: {
      ...fakeBackend(SEED),
      patch: async () => {
        throw new AppError({ code: "orm.etag_mismatch", message: "stale", httpStatus: 409 });
      },
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Edit Delivery" }));
    const description = await canvas.findByLabelText("Description, Delivery");
    await userEvent.type(description, " (express)");
    await userEvent.click(canvas.getByRole("button", { name: "Save" }));
    await expect(await canvas.findByRole("alert")).toBeTruthy();
  },
};

export const DeleteConfirm: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole("button", { name: "Delete Delivery" }));
  },
};

export const Empty: Story = { args: { seed: [] } };

export const MaxRowsReached: Story = {
  args: {
    section: { type: "sub_list", field: "line_ids", inline_edit: true, add_label: "Add product", max_rows: 3 },
  },
};

export const ReadOnlyForm: Story = { args: { formReadonly: true } };
