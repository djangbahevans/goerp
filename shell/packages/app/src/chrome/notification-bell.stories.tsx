import type { PagedResponse } from "@goerp/sdk";
import type { Notification } from "@goerp/sdk/notifications";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { NotificationBell } from "./notification-bell.js";

// Seeds the same query cache shape notification-sheet.stories.tsx uses, so
// the bell's badge count and the sheet it opens both read real data.
const withProviders: Decorator = (Story) => {
  const queryClient = new QueryClient();
  const emptyPage: PagedResponse<Notification> = { data: [], meta: { cursor: null, hasMore: false } };
  const infinite: InfiniteData<PagedResponse<Notification>> = { pages: [emptyPage], pageParams: [undefined] };
  queryClient.setQueryData(["notifications", 20], infinite);
  queryClient.setQueryData(["notifications", "unread-count"], { count: 3 });

  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <div style={{ padding: "2rem" }}>
          <Story />
        </div>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return <RouterProvider router={router} />;
};

const meta: Meta<typeof NotificationBell> = {
  title: "Chrome/NotificationBell",
  component: NotificationBell,
  decorators: [withProviders],
};

export default meta;

type Story = StoryObj<typeof NotificationBell>;

export const WithUnread: Story = {};
