import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { type ConfirmOptions, ConfirmQueue } from "../react/use-confirm.js";
import { Button } from "./button.js";
import { ConfirmDialogHost } from "./confirm-dialog-host.js";

// Each story gets its own queue, so stories never share a dialog.
function ConfirmDemo(options: ConfirmOptions) {
  const [queue] = useState(() => new ConfirmQueue());
  const [answer, setAnswer] = useState<string>("not asked");
  return (
    <div className="flex items-center gap-4">
      <Button onClick={async () => setAnswer(String(await queue.confirm(options)))}>Ask</Button>
      <span className="text-sm text-text-secondary">Answer: {answer}</span>
      <ConfirmDialogHost queue={queue} />
    </div>
  );
}

const meta = {
  title: "Actions/ConfirmDialogHost",
  component: ConfirmDemo,
  args: {
    title: "Archive this contact?",
    description: "Kwame Mensah will be hidden from all lists. You can restore them later.",
    confirmLabel: "Archive",
  },
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("button", { name: "Ask" }));
  },
} satisfies Meta<typeof ConfirmDemo>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = { args: { variant: "default" } };

export const Warning: Story = {
  args: {
    variant: "warning",
    title: "Leave without saving?",
    description: "Your changes to this invoice will be lost.",
    confirmLabel: "Leave",
  },
};

export const Danger: Story = { args: { variant: "danger" } };

export const RequireTyping: Story = {
  args: {
    variant: "danger",
    title: "Delete 47 contacts permanently?",
    description: "This cannot be undone.",
    confirmLabel: "Delete permanently",
    requireTyping: "DELETE",
  },
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("button", { name: "Ask" }));
    const dialog = within(document.body);
    const confirm = await dialog.findByRole("button", { name: "Delete permanently" });
    await expect(confirm).toBeDisabled();
    await userEvent.type(dialog.getByLabelText("Type DELETE to confirm"), "DELETE");
    await expect(confirm).toBeEnabled();
  },
};

function QueuedDemo() {
  const [queue] = useState(() => new ConfirmQueue());
  const [answers, setAnswers] = useState<string[]>([]);
  const ask = (options: ConfirmOptions) =>
    queue.confirm(options).then((answer) => setAnswers((prev) => [...prev, `${options.title} ${answer}`]));
  return (
    <div className="flex flex-col gap-2">
      <Button
        onClick={() => {
          void ask({
            title: "Archive this contact?",
            description: "You can restore them later.",
            confirmLabel: "Archive",
          });
          void ask({
            title: "Delete 3 invoices?",
            description: "This cannot be undone.",
            confirmLabel: "Delete",
            variant: "danger",
          });
        }}
      >
        Ask twice
      </Button>
      {answers.map((answer) => (
        <span key={answer} className="text-sm text-text-secondary">
          {answer}
        </span>
      ))}
      <ConfirmDialogHost queue={queue} />
    </div>
  );
}

// Two confirm() calls at once: the second dialog opens after the first is answered.
export const Queued: StoryObj<typeof QueuedDemo> = {
  render: () => <QueuedDemo />,
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("button", { name: "Ask twice" }));
  },
};
