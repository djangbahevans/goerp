import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { WifiOff } from "lucide-react";
import { ChromeBanner } from "./chrome-banner.js";

// The action is a router Link, so every story renders inside a memory router.
const withRouter: Decorator = (Story) => {
  const rootRoute = createRootRoute({ component: () => <Story /> });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  return <RouterProvider router={router} />;
};

const PASSWORD_MESSAGE =
  "Your organization has updated its password requirements. Update your password to keep your account secure.";
const PASSWORD_ACTION = { label: "Update password", to: "/settings/profile", hash: "change-password" };

const meta = {
  title: "Shell/Chrome/ChromeBanner",
  component: ChromeBanner,
  decorators: [withRouter],
  parameters: { layout: "fullscreen" },
  args: { tone: "warning", children: PASSWORD_MESSAGE },
} satisfies Meta<typeof ChromeBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const PasswordUpdate: Story = {
  args: { action: PASSWORD_ACTION, onDismiss: () => {}, dismissLabel: "Dismiss password notice" },
};

export const WarningWithoutDismiss: Story = {
  args: { icon: WifiOff, children: "You're offline. Some features may not be available." },
};

export const Info: Story = {
  args: { tone: "info", children: "Scheduled maintenance starts at 22:00 UTC.", onDismiss: () => {} },
};

export const Stacked: Story = {
  render: () => (
    <>
      <ChromeBanner tone="warning" icon={WifiOff}>
        You're offline. Some features may not be available.
      </ChromeBanner>
      <ChromeBanner tone="warning" action={PASSWORD_ACTION} onDismiss={() => {}} dismissLabel="Dismiss password notice">
        {PASSWORD_MESSAGE}
      </ChromeBanner>
    </>
  ),
};

// Below 576px of banner width the action wraps below the message.
export const Narrow: Story = {
  args: { action: PASSWORD_ACTION, onDismiss: () => {}, dismissLabel: "Dismiss password notice" },
  decorators: [
    (Story) => (
      <div style={{ width: 360 }}>
        <Story />
      </div>
    ),
  ],
};
