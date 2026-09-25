import { AuthContext, type AuthContextValue, type CurrentTenant, type CurrentUser } from "@goerp/sdk/auth";
import { Toast } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { themeStore } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { AppearancePage } from "./appearance-page.js";

const USER: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  contactId: null,
  name: "Ada Lovelace",
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system",
  locale: null,
  timezone: null,
  dateFormat: null,
};

const TENANT: CurrentTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en", "fr", "ar"],
};

function withAuth(user: CurrentUser, updatePreferences: AuthContextValue["updatePreferences"]): Decorator {
  const auth: AuthContextValue = {
    state: { status: "authenticated", user, tenant: TENANT },
    isAuthenticated: true,
    user,
    tenant: TENANT,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    updatePreferences,
    changePassword: async () => {},
    reloadSession: async () => {},
  };
  return (Story) => (
    <AuthContext.Provider value={auth}>
      <Story />
      <Toast />
    </AuthContext.Provider>
  );
}

const meta: Meta<typeof AppearancePage> = {
  title: "Settings/AppearancePage",
  component: AppearancePage,
  // A story never reloads the page after a language change.
  args: { reload: () => {} },
};

export default meta;

type Story = StoryObj<typeof AppearancePage>;

export const Defaults: Story = {
  name: "inheriting the organisation defaults",
  decorators: [withAuth(USER, async () => {})],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("combobox", { name: "Language" })).toHaveTextContent(
      "Organisation default (English)",
    );
    await expect(canvas.getByRole("combobox", { name: "Timezone" })).toHaveValue("Organisation default (UTC)");
    await expect(canvas.getByRole("combobox", { name: "Date format" })).toHaveTextContent("Automatic (from language)");
  },
};

export const Overrides: Story = {
  name: "the user's own choices",
  decorators: [
    withAuth(
      { ...USER, theme: "dark", locale: "fr", timezone: "Africa/Accra", dateFormat: "day_first" },
      async () => {},
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("combobox", { name: "Language" })).toHaveTextContent("Français");
    await expect(canvas.getByRole("combobox", { name: "Timezone" })).toHaveValue("Africa/Accra");
    await expect(canvas.getByText(/Africa\/Accra — /)).toBeInTheDocument();
  },
};

export const SaveError: Story = {
  name: "a failed save reverts the control",
  decorators: [
    withAuth(USER, async () => {
      throw new AppError({ code: "internal_error", message: "unavailable", httpStatus: 503 });
    }),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const before = themeStore.getPreference();
    await userEvent.click(canvas.getByRole("radio", { name: before === "dark" ? "Light" : "Dark" }));
    await waitFor(() => expect(themeStore.getPreference()).toBe(before));
    await waitFor(() =>
      expect(
        within(canvasElement.ownerDocument.body).getByText("Couldn't save your theme. Try again."),
      ).toBeInTheDocument(),
    );
  },
};
