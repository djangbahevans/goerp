import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, fireEvent, fn, within } from "storybook/test";
import { ListFilters } from "./list-filters.js";
import type { ListFilter } from "./list-view-types.js";

// Every declared FilterType (list-view-types.ts) resolves to a rendered
// input — the panel is far past "boolean-only" now (goerp#575, #593).
// Every input here also needs a QueryClientProvider ancestor: the
// relation/tags/user_select inputs resolve their current value's display
// label via react-query, even when no value is selected. One shared client
// for the whole file (seeded once below) — matches
// notification-sheet.stories.tsx's real-hooks-over-seeded-cache pattern —
// since Storybook composes meta- and story-level decorators rather than
// overriding, so per-story clients would double-wrap instead of replacing it.
const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } } });
// The one filter below with a pre-selected relation value — see
// useResolveFilterLabels' query key shape (list-filters.tsx).
client.setQueryData(["relation-labels", "sales.customer", "", "", ["c1"]], { c1: "Acme Corp" });

const withQueryClient: Decorator = (Story) => (
  <QueryClientProvider client={client}>
    <Story />
  </QueryClientProvider>
);

const meta: Meta<typeof ListFilters> = {
  title: "Renderers/ListFilters",
  component: ListFilters,
  decorators: [withQueryClient],
  args: {
    onChange: fn(),
  },
};

export default meta;

type Story = StoryObj<typeof ListFilters>;

// No relation/tags/user_select value is pre-selected — their label-resolution
// queries stay disabled (`enabled: ids.length > 0`), so this renders with no
// network activity at all.
const ALL_TYPES: ListFilter[] = [
  { field: "name", label: "Name", type: "text" },
  {
    field: "type",
    label: "Type",
    type: "select",
    options: [
      { value: "person", label: "Person" },
      { value: "company", label: "Company" },
    ],
  },
  {
    field: "tier",
    label: "Tier",
    type: "multi_select",
    options: [
      { value: "gold", label: "Gold" },
      { value: "silver", label: "Silver" },
    ],
  },
  {
    field: "state",
    label: "State",
    type: "radio",
    options: [
      { value: "draft", label: "Draft" },
      { value: "done", label: "Done" },
    ],
  },
  { field: "is_active", label: "Active", type: "boolean" },
  { field: "due_date", label: "Due", type: "date" },
  { field: "created_at", label: "Created", type: "daterange" },
  { field: "quantity", label: "Quantity", type: "number" },
  { field: "amount", label: "Amount", type: "number_range" },
  { field: "customer_id", label: "Customer", type: "relation", resource: "sales.customer" },
  { field: "tag_ids", label: "Tags", type: "tags", resource: "contacts.tag" },
  { field: "owner_id", label: "Owner", type: "user_select", resource: "auth.user" },
  { field: "country", label: "Country", type: "country_select" },
];

export const AllFilterTypes: Story = {
  args: { filters: ALL_TYPES, values: {} },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    for (const filter of ALL_TYPES) {
      await expect(canvas.getByText(filter.label)).toBeInTheDocument();
    }
  },
};

export const BooleanFilterToggle: Story = {
  name: "boolean: Any/Yes/No reports true/false/undefined",
  args: {
    filters: [{ field: "is_active", label: "Active", type: "boolean" }],
    values: { is_active: true },
  },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    const select = canvas.getByRole("combobox");
    await expect((select as HTMLSelectElement).value).toBe("true");

    fireEvent.change(select, { target: { value: "false" } });
    await expect(args.onChange).toHaveBeenCalledWith("is_active", false);

    fireEvent.change(select, { target: { value: "any" } });
    await expect(args.onChange).toHaveBeenCalledWith("is_active", undefined);
  },
};

export const DateAndNumberRanges: Story = {
  name: "daterange/number_range: partial bounds merge, not replace",
  args: {
    filters: [
      { field: "created_at", label: "Created", type: "daterange" },
      { field: "amount", label: "Amount", type: "number_range" },
    ],
    values: { created_at: { gte: "2026-01-01" }, amount: { gte: "10", lte: "100" } },
  },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await expect((canvas.getByLabelText("Created from") as HTMLInputElement).value).toBe("2026-01-01");
    await expect((canvas.getByLabelText("Amount min") as HTMLInputElement).value).toBe("10");

    fireEvent.change(canvas.getByLabelText("Created to"), { target: { value: "2026-02-01" } });
    await expect(args.onChange).toHaveBeenCalledWith("created_at", { gte: "2026-01-01", lte: "2026-02-01" });
  },
};

// Seeds the same react-query cache entry useResolveFilterLabels reads
// (list-filters.tsx), rather than mocking @goerp/sdk — matches
// notification-sheet.stories.tsx's own real-hooks-over-real-cache pattern.
export const RelationWithResolvedLabel: Story = {
  name: "relation: shows the selected id's resolved display label",
  args: {
    filters: [{ field: "customer_id", label: "Customer", type: "relation", resource: "sales.customer" }],
    values: { customer_id: "c1" },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("button", { name: "Clear Acme Corp" })).toBeInTheDocument();
  },
};
