import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { withAdminShell } from "../users/admin-users-story-fixtures.js";
import { ViewOverridesPage } from "./view-overrides-page.js";
import { fakeViewOverridesBackend } from "./view-overrides-story-fixtures.js";

const meta: Meta<typeof ViewOverridesPage> = {
  title: "Admin/View overrides",
  component: ViewOverridesPage,
  decorators: [withAdminShell],
  parameters: { layout: "fullscreen" },
  beforeEach: fakeViewOverridesBackend(),
};
export default meta;
type Story = StoryObj<typeof ViewOverridesPage>;

async function selectView(canvasElement: HTMLElement, name: "contacts_form" | "contacts_list") {
  const canvas = within(canvasElement);
  const picker = canvas.getByRole("combobox", { name: "View" });
  await userEvent.click(picker);
  await userEvent.type(picker, name);
  await userEvent.click(
    await within(document.body).findByRole("option", {
      name: `${name} (${name === "contacts_form" ? "form" : "list"})`,
    }),
  );
  await canvas.findByRole("table");
  return canvas;
}

export const Default: Story = {
  name: "view picker",
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("combobox", { name: "View" }));
    expect(await within(document.body).findByRole("option", { name: "contacts_form (form)" })).toBeInTheDocument();
    expect(within(document.body).getByRole("option", { name: "contacts_list (list)" })).toBeInTheDocument();
  },
};

export const Form: Story = {
  name: "form defaults and saved overrides",
  play: async ({ canvasElement }) => {
    const canvas = await selectView(canvasElement, "contacts_form");
    expect(within(canvas.getByRole("group", { name: "email" })).getByLabelText("Label")).toHaveValue("Work email");
    expect(canvas.getByText("Customised")).toBeInTheDocument();
    expect(
      within(canvas.getByRole("group", { name: "name" })).getByRole("checkbox", { name: "Required" }),
    ).toBeChecked();
  },
};

export const List: Story = {
  name: "list columns",
  play: async ({ canvasElement }) => {
    const canvas = await selectView(canvasElement, "contacts_list");
    expect(canvas.queryByRole("checkbox", { name: "Required" })).not.toBeInTheDocument();
    expect(canvas.getAllByRole("checkbox", { name: "Hidden" })).toHaveLength(3);
  },
};

export const SaveAndReset: Story = {
  name: "save a label and reset the row",
  play: async ({ canvasElement }) => {
    const canvas = await selectView(canvasElement, "contacts_form");
    const row = within(canvas.getByRole("group", { name: "name" }));
    await userEvent.clear(row.getByLabelText("Label"));
    await userEvent.type(row.getByLabelText("Label"), "Customer name");
    await userEvent.click(row.getByRole("button", { name: "Save field" }));
    await row.findByText("Customised");
    await waitFor(() => expect(row.getByRole("button", { name: "Reset to default" })).toBeEnabled());
    await userEvent.click(row.getByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(row.getByLabelText("Label")).toHaveValue("Full name"));
    expect(row.queryByText("Customised")).not.toBeInTheDocument();
  },
};

export const ValidationError: Story = {
  name: "hidden required field error",
  play: async ({ canvasElement }) => {
    const canvas = await selectView(canvasElement, "contacts_form");
    const row = within(canvas.getByRole("group", { name: "name" }));
    await userEvent.click(row.getByRole("checkbox", { name: "Hidden" }));
    await userEvent.click(row.getByRole("button", { name: "Save field" }));
    expect(await row.findByRole("alert")).toHaveTextContent('field "name" cannot be hidden while required');
  },
};

export const Phone: Story = {
  ...Form,
  name: "form at phone width",
  parameters: {
    viewport: { options: { phone390: { name: "Phone 390", styles: { width: "390px", height: "844px" } } } },
  },
  globals: { viewport: { value: "phone390", isRotated: false } },
  play: async ({ canvasElement }) => {
    await selectView(canvasElement, "contacts_form");
    expect(window.innerWidth).toBe(390);
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(390);
  },
};

export const Empty: Story = {
  name: "no views",
  beforeEach: fakeViewOverridesBackend({ empty: true }),
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "View" }));
    expect(
      await within(document.body).findByText("No views match your search.", { selector: "div" }),
    ).toBeInTheDocument();
  },
};

export const LoadError: Story = {
  name: "load error",
  beforeEach: fakeViewOverridesBackend({ failLoad: true }),
  play: async ({ canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole("combobox", { name: "View" }));
    expect(await within(document.body).findByRole("button", { name: "Retry loading views" })).toBeInTheDocument();
  },
};
