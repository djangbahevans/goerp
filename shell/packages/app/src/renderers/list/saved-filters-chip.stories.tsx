import type { SavedFilter } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, fn, userEvent, within } from "storybook/test";
import { SavedFiltersChip } from "./saved-filters-chip.js";
import type { ListStateHandle } from "./use-list-state.js";

// Real useSavedFilters over a seeded QueryClient cache, matching
// list-filters.stories.tsx's real-hooks-over-seeded-cache convention.
function clientSeededWith(viewName: string, filters: SavedFilter[]): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
  });
  client.setQueryData(["saved-filters", viewName], filters);
  return client;
}

function fakeListState(overrides: Partial<ListStateHandle> = {}): ListStateHandle {
  return {
    filter: {},
    sort: undefined,
    groupBy: undefined,
    setFilter: fn(),
    setFilters: fn(),
    setSort: fn(),
    setGroupBy: fn(),
    ...overrides,
  };
}

const POPULATED_FILTERS: SavedFilter[] = [
  {
    id: "f1",
    viewName: "contacts_list",
    label: "My Open Orders",
    queryString: "?filter[is_active]=true",
    isDefault: true,
  },
  { id: "f2", viewName: "contacts_list", label: "Archived", queryString: "?filter[is_active]=false", isDefault: false },
];

const meta: Meta<typeof SavedFiltersChip> = {
  title: "Renderers/SavedFiltersChip",
  component: SavedFiltersChip,
  args: {
    viewName: "contacts_list",
    listState: fakeListState(),
  },
};

export default meta;

type Story = StoryObj<typeof SavedFiltersChip>;

function withClient(client: QueryClient): Decorator {
  return (Story) => (
    <QueryClientProvider client={client}>
      <Story />
    </QueryClientProvider>
  );
}

export const Populated: Story = {
  decorators: [withClient(clientSeededWith("contacts_list", POPULATED_FILTERS))],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Saved filters" }));

    const body = within(document.body);
    await expect(body.getByRole("dialog", { name: "Saved filters" })).toBeInTheDocument();
    await expect(body.getByRole("button", { name: "My Open Orders" })).toBeInTheDocument();
    await expect(body.getByRole("button", { name: "Archived" })).toBeInTheDocument();
    // The already-default row has no "set as default" control.
    await expect(body.queryByRole("button", { name: "Set 'My Open Orders' as default" })).toBeNull();
    await expect(body.getByRole("button", { name: "Set 'Archived' as default" })).toBeInTheDocument();
  },
};

// Hovers rather than clicks the row actions — a real click fires
// setDefault/remove against the real apiClient, which has no backend to
// reach in Storybook and would only exercise the (already unit-tested)
// error path.
export const RowActionsVisibleOnHover: Story = {
  decorators: [withClient(clientSeededWith("contacts_list", POPULATED_FILTERS))],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Saved filters" }));

    const body = within(document.body);
    await userEvent.hover(body.getByText("Archived"));
    await expect(body.getByRole("button", { name: "Set 'Archived' as default" })).toBeVisible();
    await expect(body.getByRole("button", { name: "Delete 'Archived'" })).toBeVisible();
  },
};

export const SaveCurrentFilter: Story = {
  decorators: [withClient(clientSeededWith("contacts_list", []))],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Saved filters" }));

    const body = within(document.body);
    await expect(body.getByText("No saved filters yet.")).toBeInTheDocument();
    await userEvent.click(body.getByRole("button", { name: "Save current filter" }));
    await expect(body.getByRole("alertdialog")).toBeInTheDocument();
    await userEvent.type(body.getByLabelText("Name"), "My New Filter");
  },
};

// No Loading story: useSavedFilters' queryFn is hardcoded to the real
// apiClient, so setQueryDefaults can't intercept it — covered by
// saved-filters-chip.test.tsx's "shows a loading skeleton" test instead.
export const Empty: Story = {
  decorators: [withClient(clientSeededWith("contacts_list", []))],
};
