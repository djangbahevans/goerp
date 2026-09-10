import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { ErrorBoundary } from "./error-boundary.js";

function Thrower(): never {
  throw new Error("Something went wrong loading this section");
}

const meta: Meta<typeof ErrorBoundary> = {
  title: "Feedback/ErrorBoundary",
  component: ErrorBoundary,
};

export default meta;

type Story = StoryObj<typeof ErrorBoundary>;

export const Normal: Story = {
  args: {
    children: <p>Contact list loaded successfully.</p>,
    fallback: (error, reset) => (
      <div>
        <p className="text-danger">Failed to load contacts: {error.message}</p>
        <button type="button" onClick={reset}>
          Retry
        </button>
      </div>
    ),
  },
};

export const Errored: Story = {
  args: {
    children: <Thrower />,
    fallback: (error, reset) => (
      <div>
        <p className="text-danger">Failed to load contacts: {error.message}</p>
        <button type="button" onClick={reset}>
          Retry
        </button>
      </div>
    ),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const alert = canvas.getByRole("alert");
    await expect(alert).toHaveTextContent("Something went wrong loading this section");
    await expect(canvas.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  },
};
