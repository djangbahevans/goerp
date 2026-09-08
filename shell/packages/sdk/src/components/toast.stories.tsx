import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { ToastBus } from "../notifications/toast.js";
import { Toast } from "./toast.js";

const meta: Meta<typeof Toast> = {
  title: "Feedback/Toast",
  component: Toast,
};

export default meta;

type Story = StoryObj<typeof Toast>;

// Seeds a fresh ToastBus (and starts its auto-dismiss timers) at mount
// time via the useState initializer, not at story-module-eval time. A
// story's `args` object is built once when the CSF file is first
// imported — a ToastBus created there starts its real setTimeout timers
// immediately, so by the time someone actually navigates to and mounts
// the story (seconds to minutes later, depending on browsing), a
// success/info toast's 4s window (or warning's 6s, error's 8s) may
// already have elapsed, rendering nothing. Building the bus inside the
// component instead ties its timers to actual mount time, so the toast
// is guaranteed visible on every view.
function ToastDemo({ setup }: { setup: (bus: ToastBus) => void }) {
  const [bus] = useState(() => {
    const b = new ToastBus();
    setup(b);
    return b;
  });
  return <Toast bus={bus} />;
}

export const Stacked: Story = {
  render: () => (
    <ToastDemo
      setup={(bus) => {
        bus.success("Contact saved");
        bus.warning("This record was recently changed by someone else");
        bus.error("Couldn't reach the server", { action: { label: "Retry", onClick: () => {} } });
        bus.loading("Uploading attachment…");
      }}
    />
  ),
};

export const Success: Story = {
  render: () => <ToastDemo setup={(bus) => bus.success("Contact saved")} />,
};

export const ErrorWithAction: Story = {
  render: () => (
    <ToastDemo setup={(bus) => bus.error("Couldn't save", { action: { label: "Retry", onClick: () => {} } })} />
  ),
};

export const Warning: Story = {
  render: () => <ToastDemo setup={(bus) => bus.warning("This record was recently changed by someone else")} />,
};

export const Info: Story = {
  render: () => <ToastDemo setup={(bus) => bus.info("This module is in beta")} />,
};

export const Loading: Story = {
  render: () => <ToastDemo setup={(bus) => bus.loading("Uploading attachment…")} />,
};
