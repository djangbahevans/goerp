import type { Meta, StoryObj } from "@storybook/react-vite";
import { fn } from "storybook/test";
import { SessionExpiredModal } from "./session-expired-modal.js";

const meta = {
  title: "Shell/Auth/SessionExpiredModal",
  component: SessionExpiredModal,
  parameters: { layout: "fullscreen" },
  args: { onSignIn: fn() },
  decorators: [
    (Story) => (
      <>
        <div className="p-6 text-text">
          <h1 className="font-semibold text-xl">Contacts</h1>
          <p className="mt-2 text-sm text-text-secondary">The page the session expired on stays behind the modal.</p>
        </div>
        <Story />
      </>
    ),
  ],
} satisfies Meta<typeof SessionExpiredModal>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
