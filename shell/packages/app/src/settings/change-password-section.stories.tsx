import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { ChangePasswordSection } from "./change-password-section.js";

const fakeUser = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
};
const fakeTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

function authWith(changePassword: AuthContextValue["changePassword"]): AuthContextValue {
  return {
    state: { status: "authenticated", user: fakeUser, tenant: fakeTenant },
    isAuthenticated: true,
    user: fakeUser,
    tenant: fakeTenant,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile: async () => {},
    changePassword,
  };
}

function withProviders(auth: AuthContextValue): Decorator {
  return (Story) => {
    const rootRoute = createRootRoute({
      component: () => (
        <AuthContext.Provider value={auth}>
          <div className="max-w-md p-6">
            <Story />
          </div>
        </AuthContext.Provider>
      ),
    });
    const router = createRouter({
      routeTree: rootRoute,
      history: createMemoryHistory({ initialEntries: ["/settings/profile"] }),
    });
    return <RouterProvider router={router} />;
  };
}

const meta = {
  title: "Shell/Settings/ChangePasswordSection",
  component: ChangePasswordSection,
} satisfies Meta<typeof ChangePasswordSection>;

export default meta;
type Story = StoryObj<typeof meta>;

async function fillAndSubmit(canvasElement: HTMLElement, current: string, next: string, confirm: string) {
  const canvas = within(canvasElement);
  // RouterProvider renders asynchronously, after play starts.
  await userEvent.type(await canvas.findByLabelText("Current password"), current);
  await userEvent.type(canvas.getByLabelText("New password"), next);
  await userEvent.type(canvas.getByLabelText("Confirm new password"), confirm);
  await userEvent.click(canvas.getByRole("button", { name: "Change password" }));
  return canvas;
}

export const Idle: Story = {
  decorators: [withProviders(authWith(async () => {}))],
};

export const Validation: Story = {
  decorators: [withProviders(authWith(async () => {}))],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement, "old passphrase here", "a brand new passphrase", "typo");
    await expect(await canvas.findByText("Passwords don't match.")).toBeInTheDocument();
  },
};

export const Submitting: Story = {
  decorators: [withProviders(authWith(() => new Promise<void>(() => {})))],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(
      canvasElement,
      "old passphrase here",
      "a brand new passphrase",
      "a brand new passphrase",
    );
    await expect(canvas.getByRole("button", { name: "Change password" })).toHaveAttribute("aria-busy", "true");
  },
};

export const WrongCurrentPassword: Story = {
  decorators: [
    withProviders(
      authWith(async () => {
        throw new AppError({ code: "invalid_password", message: "current password is incorrect", httpStatus: 401 });
      }),
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement, "wrong", "a brand new passphrase", "a brand new passphrase");
    await expect(await canvas.findByText("Current password is incorrect.")).toBeInTheDocument();
  },
};

export const TooWeak: Story = {
  decorators: [
    withProviders(
      authWith(async () => {
        throw new AppError({ code: "auth.password_too_weak", message: "password is too common", httpStatus: 422 });
      }),
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(canvasElement, "old passphrase here", "schmetterling", "schmetterling");
    await expect(await canvas.findByText("Password is too common.")).toBeInTheDocument();
  },
};

export const Success: Story = {
  decorators: [withProviders(authWith(async () => {}))],
  play: async ({ canvasElement }) => {
    const canvas = await fillAndSubmit(
      canvasElement,
      "old passphrase here",
      "a brand new passphrase",
      "a brand new passphrase",
    );
    await expect(canvas.getByLabelText("Current password")).toHaveValue("");
  },
};
