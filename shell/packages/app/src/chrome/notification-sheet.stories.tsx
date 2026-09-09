import type { PagedResponse } from "@goerp/sdk";
import type { Notification } from "@goerp/sdk/notifications";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { NotificationSheet } from "./notification-sheet.js";

const NOW = Date.now();
const exampleNotifications: Notification[] = [
  {
    id: "n1",
    type: "sales.order_confirmed",
    module: "sales",
    title: "Order #1042 confirmed",
    body: "Payment received and the order has moved to fulfillment.",
    actionUrl: "/sales/orders/1042",
    icon: null,
    readAt: null,
    createdAt: new Date(NOW - 5 * 60_000).toISOString(),
  },
  {
    id: "n2",
    type: "inventory.low_stock",
    module: "inventory",
    title: "Low stock: Blue T-Shirt (M)",
    body: null,
    actionUrl: "/inventory/products/blue-tshirt-m",
    icon: null,
    readAt: new Date(NOW - 3600_000).toISOString(),
    createdAt: new Date(NOW - 3 * 3600_000).toISOString(),
  },
  {
    id: "n3",
    type: "system.maintenance",
    module: "system",
    title: "Scheduled maintenance completed",
    body: null,
    actionUrl: null,
    icon: null,
    readAt: new Date(NOW - 86_400_000).toISOString(),
    createdAt: new Date(NOW - 90_000_000).toISOString(),
  },
];

// Seeds the query cache directly rather than mocking network calls — the
// component reads through real useNotifications()/useUnreadCount(), just
// against a QueryClient pre-populated with this story's fixture data.
const withProviders: Decorator = (Story) => {
  const queryClient = new QueryClient();
  const page: PagedResponse<Notification> = { data: exampleNotifications, meta: { cursor: null, hasMore: false } };
  const infinite: InfiniteData<PagedResponse<Notification>> = { pages: [page], pageParams: [undefined] };
  queryClient.setQueryData(["notifications", 20], infinite);
  queryClient.setQueryData(["notifications", "unread-count"], { count: 1 });

  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <button type="button" data-testid="background-button">
          Background page content
        </button>
        <Story />
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return <RouterProvider router={router} />;
};

const meta: Meta<typeof NotificationSheet> = {
  title: "Chrome/NotificationSheet",
  component: NotificationSheet,
  decorators: [withProviders],
  args: {
    open: true,
    onClose: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof NotificationSheet>;

export const Opened: Story = {
  play: async ({ canvasElement }) => {
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => body.getByRole("dialog", { name: "Notifications" }));

    body.getByText("Order #1042 confirmed");
    body.getByText("Low stock: Blue T-Shirt (M)");

    // Focus starts on the panel's own heading, not the first row.
    await expect(body.getByText("Notifications")).toHaveFocus();
  },
};

// Confirms the panel is genuinely non-modal: Tab eventually reaches page
// content behind it instead of being trapped inside the panel.
export const NonModalTabPassesThrough: Story = {
  play: async ({ canvasElement }) => {
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => body.getByRole("dialog", { name: "Notifications" }));

    for (let i = 0; i < 8; i++) {
      await userEvent.tab();
      if (document.activeElement === body.getByTestId("background-button")) break;
    }
    await expect(body.getByTestId("background-button")).toHaveFocus();
  },
};
