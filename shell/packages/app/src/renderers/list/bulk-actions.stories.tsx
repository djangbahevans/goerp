import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { useBulkAction } from "@goerp/sdk/react";
import { componentRegistry } from "@goerp/sdk/schema";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, fn, userEvent, within } from "storybook/test";
import { BulkActions } from "./bulk-actions.js";
import type { BulkAction } from "./list-view-types.js";

// view-system.md's "Bulk actions" — only custom/route/export are rendered
// (list-actions.tsx's header_actions convention: create/import/report are
// left unimplemented rather than half-built).
const ACTIONS: BulkAction[] = [
  { label: "Add Tag", type: "custom", component: "storybook.bulk-actions.TagPanel" },
  {
    label: "Archive",
    type: "route",
    route: "sales.archiveOrders",
    style: "danger",
    confirm: { title: "Archive orders?", message: "This cannot be undone.", destructive: true },
  },
  { label: "Export Selected", type: "export", route: "sales.exportOrders" },
  // Deliberately out of the rendered set — confirms the "only custom/route/export" claim above.
  { label: "Import", type: "import" },
];

// A stand-in for a module's real bulk-action component (defineModule's
// component registration doesn't exist in this SDK yet — see
// component-registry.ts) — shows the selection context ActiveCustomPanel
// wires through BulkActionContext.
function TagPanel() {
  const { selectedIds, selectedCount, onComplete, onCancel } = useBulkAction();
  return (
    <div>
      <p>{selectedCount} orders selected</p>
      <p>{selectedIds.join(", ")}</p>
      <button type="button" onClick={onComplete}>
        Apply Tag
      </button>
      <button type="button" onClick={onCancel}>
        Cancel
      </button>
    </div>
  );
}
if (!componentRegistry.has("storybook.bulk-actions.TagPanel")) {
  componentRegistry.register("storybook.bulk-actions.TagPanel", TagPanel);
}

function permissionDecorator(permissions: string[]): Decorator {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return (Story) => (
    <PermissionContext.Provider value={value}>
      <Story />
    </PermissionContext.Provider>
  );
}

// The "route"/"export" action buttons call useAction()/useExport(), both
// backed by useMutation — a QueryClient ancestor is required just to
// construct the mutation (before it's ever fired), same as
// list-filters.stories.tsx's withQueryClient.
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const withQueryClient: Decorator = (Story) => (
  <QueryClientProvider client={queryClient}>
    <Story />
  </QueryClientProvider>
);

const meta: Meta<typeof BulkActions> = {
  title: "Renderers/BulkActions",
  component: BulkActions,
  decorators: [withQueryClient],
  args: {
    actions: ACTIONS,
    selectedIds: ["order-1", "order-2"],
    clearSelection: fn(),
  },
};

export default meta;

type Story = StoryObj<typeof BulkActions>;

export const Default: Story = {
  name: "custom/route/export, import omitted",
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("2 selected")).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Add Tag" })).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Archive" })).toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Export Selected" })).toBeInTheDocument();
    await expect(canvas.queryByText("Import")).not.toBeInTheDocument();
  },
};

export const NoSelection: Story = {
  name: "Nothing selected renders nothing",
  args: { selectedIds: [] },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // Not an empty canvasElement.innerHTML — the global withTheme decorator
    // (preview.tsx) always wraps every story in its own padded div.
    await expect(canvas.queryByRole("toolbar")).not.toBeInTheDocument();
    await expect(canvas.queryByText(/selected/)).not.toBeInTheDocument();
  },
};

export const PermissionGated: Story = {
  name: "permission-gated action hidden without it",
  args: {
    actions: [
      { label: "Add Tag", type: "custom", component: "storybook.bulk-actions.TagPanel" },
      { label: "Cancel Orders", type: "route", route: "sales.cancelOrders", permission: "sales:order:cancel" },
    ],
  },
  decorators: [permissionDecorator([])],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("button", { name: "Add Tag" })).toBeInTheDocument();
    await expect(canvas.queryByRole("button", { name: "Cancel Orders" })).not.toBeInTheDocument();
  },
};

// Two independent instances, side by side, so min_selected/max_selected
// gating is visible by direct comparison rather than a before/after toggle.
export const SelectionCountGating: Story = {
  name: "min_selected/max_selected hide the action outside its range",
  render: () => {
    const rangedAction: BulkAction = {
      label: "Merge",
      type: "route",
      route: "sales.mergeOrders",
      min_selected: 2,
      max_selected: 3,
    };
    return (
      <div style={{ display: "grid", gap: "1rem" }}>
        <section>
          <p>1 selected (below min_selected: 2) — hidden</p>
          <BulkActions actions={[rangedAction]} selectedIds={["order-1"]} clearSelection={() => {}} />
        </section>
        <section>
          <p>2 selected (within range) — visible</p>
          <BulkActions actions={[rangedAction]} selectedIds={["order-1", "order-2"]} clearSelection={() => {}} />
        </section>
        <section>
          <p>4 selected (above max_selected: 3) — hidden</p>
          <BulkActions
            actions={[rangedAction]}
            selectedIds={["order-1", "order-2", "order-3", "order-4"]}
            clearSelection={() => {}}
          />
        </section>
      </div>
    );
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getAllByRole("button", { name: "Merge" })).toHaveLength(1);
  },
};

// Confirms the confirm/destructive dialog appears without firing the
// underlying mutation — clicking "Confirm" would call the real route
// action's mutate() against a live backend this story has none of.
// AlertDialog renders via Radix's Portal (alert-dialog.tsx), straight onto
// document.body rather than inside canvasElement — same as
// notification-sheet.stories.tsx's own portaled-dialog queries.
export const RouteConfirm: Story = {
  name: "route action's confirm dialog",
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(canvas.getByRole("button", { name: "Archive" }));
    await expect(body.getByText("Archive orders?")).toBeInTheDocument();
    await expect(body.getByText("This cannot be undone.")).toBeInTheDocument();
    await expect(body.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
  },
};

// The custom bulk action's full lifecycle: activate → registered component
// mounts with selection context → Complete calls the real clearSelection
// prop and returns to the action bar.
export const CustomActionPanel: Story = {
  name: "custom action mounts its registered component",
  args: { clearSelection: fn() },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Add Tag" }));

    await expect(canvas.getByText("2 orders selected")).toBeInTheDocument();
    await expect(canvas.getByText("order-1, order-2")).toBeInTheDocument();
    await expect(canvas.queryByRole("button", { name: "Add Tag" })).not.toBeInTheDocument();

    await userEvent.click(canvas.getByRole("button", { name: "Apply Tag" }));
    await expect(canvas.getByRole("button", { name: "Add Tag" })).toBeInTheDocument();
    await expect(args.clearSelection).toHaveBeenCalledTimes(1);
  },
};
