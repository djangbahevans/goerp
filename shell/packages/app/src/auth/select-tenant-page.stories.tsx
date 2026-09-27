import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { SelectTenantPage } from "./select-tenant-page.js";

// The pick never settles, so a story can hold on the in-progress state.
const pendingPick: AuthContextValue = {
  state: { status: "unauthenticated" },
  isAuthenticated: false,
  user: null,
  tenant: null,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: () => new Promise(() => {}),
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
  expireSession: () => {},
};

const withProviders: Decorator = (Story) => {
  const rootRoute = createRootRoute({
    component: () => (
      <AuthContext.Provider value={pendingPick}>
        <Story />
      </AuthContext.Provider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/auth/select-tenant"] }),
  });
  return <RouterProvider router={router} />;
};

const meta = {
  title: "Shell/Auth/SelectTenantPage",
  component: SelectTenantPage,
  args: {
    redirectTo: "/",
    selection: {
      tenants: [
        { slug: "acme", name: "Acme Corp" },
        { slug: "globex", name: "Globex Corporation" },
        { slug: "initech", name: "Initech" },
      ],
      selectionToken: "tok",
    },
  },
  parameters: { layout: "fullscreen" },
  decorators: [withProviders],
} satisfies Meta<typeof SelectTenantPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const heading = await canvas.findByRole("heading", { name: "Choose an organisation" });
    await expect(heading).toHaveFocus();
    await expect(canvas.getAllByRole("button")).toHaveLength(3);
  },
};

export const Choosing: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const globex = await canvas.findByRole("button", { name: /Globex/ });
    await userEvent.click(globex);
    await expect(globex).toHaveAttribute("aria-busy", "true");
    await expect(canvas.getByRole("button", { name: /Acme Corp/ })).toBeDisabled();
  },
};

export const LongNames: Story = {
  args: {
    selection: {
      tenants: [
        {
          slug: "north-atlantic-regional-logistics-and-freight",
          name: "North Atlantic Regional Logistics and Freight Holdings",
        },
        { slug: "acme", name: "Acme Corp" },
      ],
      selectionToken: "tok",
    },
  },
};
