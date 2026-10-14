import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { Button } from "../components/button.js";
import { TextInput } from "../components/text-input.js";
import { resourceRef } from "../names.js";
import { simulateRealtimeMessage } from "../testing/simulate-realtime-message.js";
import { usePresence } from "./use-presence.js";

const contact = resourceRef<{ id: string; name: string; email: string }>("contacts.contact");
const channel = "record:contacts.contact:presence-demo";
const ama = {
  user_id: "ama",
  name: "Ama Mensah",
  avatar_url: null,
  editing_field: null,
  joined_at: "2026-10-14T09:00:00Z",
  last_seen_at: "2026-10-14T09:00:30Z",
};

function PresenceDemo() {
  const [open, setOpen] = useState(true);
  function publish(editingField: string | null, present = true) {
    simulateRealtimeMessage(channel, {
      type: "custom",
      event: "presence.update",
      payload: {
        resource: contact,
        record_id: "presence-demo",
        users: present ? [{ ...ama, editing_field: editingField }] : [],
      },
    });
  }
  return (
    <div className="flex max-w-lg flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => publish(null)}>Ama joins</Button>
        <Button onClick={() => publish("email")}>Ama edits email</Button>
        <Button onClick={() => publish(null, false)}>Ama leaves</Button>
        <Button onClick={() => setOpen((value) => !value)}>{open ? "Close record" : "Open record"}</Button>
      </div>
      {open && <RecordPresence />}
    </div>
  );
}

function RecordPresence() {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [editingField, setEditingField] = useState<"name" | "email" | null>(null);
  const users = usePresence(contact, "presence-demo", { editingField });
  return (
    <section className="flex flex-col gap-4" aria-label="Contact">
      <label className="flex flex-col gap-1" htmlFor="presence-name">
        Name
        <TextInput
          id="presence-name"
          value={name}
          onChange={setName}
          onFocus={() => setEditingField("name")}
          onBlur={() => setEditingField(null)}
        />
      </label>
      <label className="flex flex-col gap-1" htmlFor="presence-email">
        Email
        <TextInput
          id="presence-email"
          value={email}
          onChange={setEmail}
          onFocus={() => setEditingField("email")}
          onBlur={() => setEditingField(null)}
        />
      </label>
      <output aria-label="Present users">
        {users.length === 0
          ? "No collaborators"
          : users
              .map((user) => `${user.name}${user.editingField ? ` is editing ${user.editingField}` : " is viewing"}`)
              .join(", ")}
      </output>
    </section>
  );
}

const meta: Meta<typeof PresenceDemo> = { title: "Realtime/usePresence", component: PresenceDemo };
export default meta;
type Story = StoryObj<typeof PresenceDemo>;

export const RecordLifecycle: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("status")).toHaveTextContent("No collaborators");
    await userEvent.click(canvas.getByRole("button", { name: "Ama joins" }));
    await waitFor(() => expect(canvas.getByRole("status")).toHaveTextContent("Ama Mensah is viewing"));
    await userEvent.click(canvas.getByRole("textbox", { name: "Name" }));
    await expect(canvas.getByRole("textbox", { name: "Name" })).toHaveFocus();
    await userEvent.tab();
    await expect(canvas.getByRole("textbox", { name: "Email" })).toHaveFocus();
    await userEvent.click(canvas.getByRole("button", { name: "Ama edits email" }));
    await waitFor(() => expect(canvas.getByRole("status")).toHaveTextContent("Ama Mensah is editing email"));
    await userEvent.click(canvas.getByRole("button", { name: "Ama leaves" }));
    await waitFor(() => expect(canvas.getByRole("status")).toHaveTextContent("No collaborators"));
    await userEvent.click(canvas.getByRole("button", { name: "Ama joins" }));
    await userEvent.click(canvas.getByRole("button", { name: "Close record" }));
    await expect(canvas.queryByRole("region", { name: "Contact" })).not.toBeInTheDocument();
    await userEvent.click(canvas.getByRole("button", { name: "Open record" }));
    await expect(canvas.getByRole("status")).toHaveTextContent("No collaborators");
  },
};
