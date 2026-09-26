import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, within } from "storybook/test";
import { HandoffPage } from "./handoff-page.js";

// The exchange never settles, so the story holds on the in-progress state.
const pendingExchange: AuthContextValue = {
  state: { status: "unauthenticated" },
  isAuthenticated: false,
  user: null,
  tenant: null,
  login: async () => null,
  completeHandoff: () => new Promise(() => {}),
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

const withProviders: Decorator = (Story) => {
  const rootRoute = createRootRoute({
    component: () => (
      <AuthContext.Provider value={pendingExchange}>
        <Story />
      </AuthContext.Provider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/auth/handoff?code=c0de"] }),
  });
  return <RouterProvider router={router} />;
};

const meta = {
  title: "Shell/Auth/HandoffPage",
  component: HandoffPage,
  args: { code: "c0de", redirectTo: "/" },
  parameters: { layout: "fullscreen" },
  decorators: [withProviders],
} satisfies Meta<typeof HandoffPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SigningIn: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // RouterProvider renders asynchronously, after play starts.
    await expect(await canvas.findByRole("status")).toHaveTextContent("Signing you in…");
  },
};
