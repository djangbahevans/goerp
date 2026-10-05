import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ComponentProps } from "react";
import { useState } from "react";
import { userEvent, within } from "storybook/test";
import { SignaturePad } from "./signature-pad.js";

const meta: Meta<typeof SignaturePad> = {
  title: "Form Fields/SignaturePad",
  component: SignaturePad,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof SignaturePad>;

export const Empty: Story = {
  args: {
    value: null,
  },
};

export const Captured: Story = {
  args: {
    // A 1x1 transparent PNG, standing in for a real captured drawing.
    value:
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  },
};

export const Disabled: Story = {
  args: {
    value:
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
    disabled: true,
  },
};

const mockUpload: NonNullable<ComponentProps<typeof SignaturePad>["uploadFn"]> = () => ({
  promise: new Promise((resolve) =>
    setTimeout(
      () => resolve({ fileId: "01jstorybook", name: "signature.png", contentType: "image/png", sizeBytes: 1024 }),
      300,
    ),
  ),
  abort: () => {},
});

function Interactive(args: ComponentProps<typeof SignaturePad>) {
  const [value, setValue] = useState<string | null>(args.value);
  return <SignaturePad {...args} value={value} onChange={setValue} />;
}

export const DrawAndCapture: Story = {
  args: { value: null, uploadFn: mockUpload },
  render: (args) => <Interactive {...args} />,
};

export const TypeMode: Story = {
  args: { value: null, signerName: "Kofi Mensah", uploadFn: mockUpload },
  render: (args) => <Interactive {...args} />,
  play: async ({ canvasElement }) => {
    const body = within(canvasElement);
    await userEvent.click(body.getByRole("radio", { name: "Type" }));
  },
};

export const TypeModeLongName: Story = {
  args: {
    value: null,
    signerName: "Wolfeschlegelsteinhausenbergerdorff Mensah-Asante",
    uploadFn: mockUpload,
  },
  render: (args) => <Interactive {...args} />,
  play: async ({ canvasElement }) => {
    const body = within(canvasElement);
    await userEvent.click(body.getByRole("radio", { name: "Type" }));
  },
};
