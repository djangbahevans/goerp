import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { FieldWrapper } from "./field-wrapper.js";
import { SectionCard } from "./section-card.js";
import { TextInput } from "./text-input.js";

const meta: Meta<typeof SectionCard> = {
  title: "Layout/SectionCard",
  component: SectionCard,
};

export default meta;

type Story = StoryObj<typeof SectionCard>;

export const Default: Story = {
  args: {
    title: "Contact Information",
    children: <p>Fields go here</p>,
  },
};

export const Collapsible: Story = {
  args: {
    title: "Employment Details",
    collapsible: true,
    children: <p>Fields go here</p>,
  },
};

export const CollapsedByDefault: Story = {
  args: {
    title: "Employment Details",
    collapsible: true,
    defaultCollapsed: true,
    children: <p>Fields go here</p>,
  },
};

export const Untitled: Story = {
  args: {
    children: <p>Fields go here</p>,
  },
};

const noop = () => {};

export const TextInputsInLastRow: Story = {
  args: {
    title: "Contact Information",
    collapsible: true,
    children: (
      <div className="grid grid-cols-2 gap-4">
        <FieldWrapper label="Name">
          <TextInput value="Kofi Mensah" onChange={noop} />
        </FieldWrapper>
        <FieldWrapper label="Email">
          <TextInput value="kofi@example.test" onChange={noop} />
        </FieldWrapper>
        <FieldWrapper label="Phone">
          <TextInput value="+233 20 000 0000" onChange={noop} />
        </FieldWrapper>
        <FieldWrapper label="City">
          <TextInput value="Accra" onChange={noop} />
        </FieldWrapper>
      </div>
    ),
  },
  play: async ({ canvasElement }) => {
    // Measured on the first frame: a section that grows in on mount is
    // still short of its content here.
    await new Promise(requestAnimationFrame);
    const region = canvasElement.querySelector("section > div[id]");
    if (!(region instanceof HTMLElement)) throw new Error("section content region not found");
    const regionBottom = region.getBoundingClientRect().bottom;
    for (const input of within(canvasElement).getAllByRole("textbox")) {
      await expect(input.getBoundingClientRect().bottom).toBeLessThanOrEqual(regionBottom);
    }
  },
};
