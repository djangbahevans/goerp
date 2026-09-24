import type { AuthContextValue, MFAMethod } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { MFAChallengePage } from "./mfa-challenge-page.js";

function authWith(methods: MFAMethod[], submitMFA: AuthContextValue["submitMFA"] = async () => {}): AuthContextValue {
  return {
    state: { status: "mfa_required", challengeToken: "tok", methods },
    isAuthenticated: false,
    user: null,
    tenant: null,
    login: async () => {},
    logout: async () => {},
    submitMFA,
    updateProfile: async () => {},
    changePassword: async () => {},
  };
}

function withProviders(auth: AuthContextValue): Decorator {
  return (Story) => {
    const rootRoute = createRootRoute({
      component: () => (
        <AuthContext.Provider value={auth}>
          <Story />
        </AuthContext.Provider>
      ),
    });
    const router = createRouter({
      routeTree: rootRoute,
      history: createMemoryHistory({ initialEntries: ["/auth/mfa"] }),
    });
    return <RouterProvider router={router} />;
  };
}

const meta = {
  title: "Shell/Auth/MFAChallengePage",
  component: MFAChallengePage,
  args: { redirectTo: "/" },
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof MFAChallengePage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AuthenticatorApp: Story = {
  decorators: [withProviders(authWith(["totp", "recovery_code"]))],
};

export const RecoveryCode: Story = {
  decorators: [withProviders(authWith(["totp", "recovery_code"]))],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // RouterProvider renders asynchronously, after play starts.
    await userEvent.click(await canvas.findByRole("button", { name: "Use a recovery code instead" }));
    await expect(canvas.getByLabelText("Recovery code")).toHaveFocus();
  },
};

export const RecoveryCodeOnly: Story = {
  decorators: [withProviders(authWith(["recovery_code"]))],
};

export const NetworkFailure: Story = {
  decorators: [
    withProviders(
      authWith(["totp", "recovery_code"], async () => {
        throw new TypeError("Failed to fetch");
      }),
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(await canvas.findByLabelText("Verification code"), "123456");
    await expect(await canvas.findByText(/Check your connection/)).toBeVisible();
  },
};

export const UnsupportedMethod: Story = {
  decorators: [withProviders(authWith(["webauthn"]))],
};
