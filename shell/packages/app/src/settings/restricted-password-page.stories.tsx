import { AuthContext, type AuthContextValue } from "@goerp/sdk/auth";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, within } from "storybook/test";
import { RestrictedPasswordPage } from "./restricted-password-page.js";

const user = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada",
  avatarUrl: null,
  contactId: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  passwordChangeRequired: true,
  passwordMinLength: 18,
  phone: null,
  title: null,
  theme: "system" as const,
  contrast: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
  passwordMinLength: 14,
};
const auth: AuthContextValue = {
  state: { status: "authenticated", user, tenant },
  isAuthenticated: true,
  user,
  tenant,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: async () => null,
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
  expireSession: () => {},
};

const meta = {
  title: "Shell/Settings/RestrictedPasswordPage",
  component: RestrictedPasswordPage,
  parameters: { layout: "fullscreen" },
  decorators: [
    (Story) => {
      const root = createRootRoute({
        component: () => (
          <AuthContext.Provider value={auth}>
            <Story />
          </AuthContext.Provider>
        ),
      });
      const router = createRouter({
        routeTree: root,
        history: createMemoryHistory({ initialEntries: ["/settings/profile#change-password"] }),
      });
      return <RouterProvider router={router} />;
    },
  ],
} satisfies Meta<typeof RestrictedPasswordPage>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Required: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(await canvas.findByRole("heading", { name: "Change your password to keep using Acme" })).toBeVisible();
    await expect(canvas.getByLabelText("Current password")).toHaveFocus();
    await expect(canvas.getByLabelText("New password")).toHaveAttribute("minlength", "18");
    await expect(canvas.getByRole("button", { name: "Sign out" })).toBeVisible();
  },
};
