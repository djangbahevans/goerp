import type { Meta, StoryObj } from "@storybook/react-vite";
import { ToastBus } from "../notifications/toast.js";
import { Toast } from "./toast.js";

const meta: Meta<typeof Toast> = {
  title: "Feedback/Toast",
  component: Toast,
};

export default meta;

type Story = StoryObj<typeof Toast>;

function busWith(setup: (bus: ToastBus) => void): ToastBus {
  const bus = new ToastBus();
  setup(bus);
  return bus;
}

export const Stacked: Story = {
  args: {
    bus: busWith((bus) => {
      bus.success("Contact saved");
      bus.warning("This record was recently changed by someone else");
      bus.error("Couldn't reach the server", { action: { label: "Retry", onClick: () => {} } });
      bus.loading("Uploading attachment…");
    }),
  },
};

export const Success: Story = {
  args: {
    bus: busWith((bus) => bus.success("Contact saved")),
  },
};

export const ErrorWithAction: Story = {
  args: {
    bus: busWith((bus) => bus.error("Couldn't save", { action: { label: "Retry", onClick: () => {} } })),
  },
};

export const Warning: Story = {
  args: {
    bus: busWith((bus) => bus.warning("This record was recently changed by someone else")),
  },
};

export const Info: Story = {
  args: {
    bus: busWith((bus) => bus.info("This module is in beta")),
  },
};

export const Loading: Story = {
  args: {
    bus: busWith((bus) => bus.loading("Uploading attachment…")),
  },
};
