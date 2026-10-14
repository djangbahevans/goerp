import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import { Button } from "../components/button.js";
import { FieldWrapper } from "../components/field-wrapper.js";
import { TextInput } from "../components/text-input.js";
import { useForm, validators } from "./index.js";

function ContactForm() {
  const [saved, setSaved] = useState("");
  const form = useForm({
    defaultValues: { name: "" },
    validate: ({ name }) => {
      const error = validators.required(name);
      return error === undefined ? {} : { name: error };
    },
  });

  return (
    <form className="flex w-80 flex-col gap-4" onSubmit={form.handleSubmit(({ name }) => setSaved(name))}>
      <FieldWrapper label="Name" error={form.errors.name}>
        <TextInput {...form.register("name")} />
      </FieldWrapper>
      <div className="flex gap-2">
        <Button type="submit" variant="primary">
          Save
        </Button>
        <Button
          type="button"
          onClick={() => {
            form.reset();
            setSaved("");
          }}
        >
          Reset
        </Button>
        <Button type="button" onClick={() => form.setErrors({ name: ["Name already exists"] })}>
          Server error
        </Button>
      </div>
      <output aria-label="Form status">
        {saved ? `Saved ${saved}` : form.isDirty ? "Unsaved changes" : "No changes"}
      </output>
    </form>
  );
}

const meta: Meta<typeof ContactForm> = { title: "Form/useForm", component: ContactForm };
export default meta;
type Story = StoryObj<typeof ContactForm>;

export const ValidationAndReset: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole("textbox", { name: "Name" });
    await userEvent.click(canvas.getByRole("button", { name: "Save" }));
    await expect(canvas.getByText("Required")).toBeVisible();
    await expect(canvas.getByRole("status")).toHaveTextContent("No changes");
    await userEvent.type(input, "Ama");
    await expect(input).toHaveFocus();
    await expect(canvas.queryByText("Required")).not.toBeInTheDocument();
    await expect(canvas.getByRole("status")).toHaveTextContent("Unsaved changes");
    await userEvent.click(canvas.getByRole("button", { name: "Save" }));
    await expect(canvas.getByRole("status")).toHaveTextContent("Saved Ama");
    await userEvent.click(canvas.getByRole("button", { name: "Server error" }));
    await expect(canvas.getByText("Name already exists")).toBeVisible();
    await expect(input).toHaveAttribute("aria-invalid", "true");
    await userEvent.click(canvas.getByRole("button", { name: "Reset" }));
    await expect(input).toHaveValue("");
    await expect(canvas.queryByText("Name already exists")).not.toBeInTheDocument();
    await expect(canvas.getByRole("status")).toHaveTextContent("No changes");
  },
};
