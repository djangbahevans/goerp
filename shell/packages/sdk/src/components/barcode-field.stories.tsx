import type { Meta, StoryObj } from "@storybook/react-vite";
import { userEvent, within } from "storybook/test";
import { BarcodeField } from "./barcode-field.js";

const meta: Meta<typeof BarcodeField> = {
  title: "Form Fields/BarcodeField",
  component: BarcodeField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof BarcodeField>;

export const Default: Story = {
  args: {
    value: "",
  },
};

export const WithValue: Story = {
  args: {
    value: "9781234567897",
  },
};

// A real getUserMedia call in Storybook's own iframe would hit an actual
// browser permission prompt (or, in a sandboxed/headless context, an
// automatic denial) rather than reliably opening the overlay — stubbed
// here with a real MediaStream (a blank canvas's own captureStream())
// so the scanning overlay's video element has something genuine to
// attach to, the same way a real (if pointed at nothing meaningful)
// camera feed would.
async function stubCameraAccess(resolves: boolean) {
  const canvas = document.createElement("canvas");
  canvas.width = 640;
  canvas.height = 480;
  navigator.mediaDevices.getUserMedia = resolves
    ? () => Promise.resolve((canvas as HTMLCanvasElement & { captureStream(): MediaStream }).captureStream())
    : () => Promise.reject(new DOMException("Permission denied", "NotAllowedError"));
}

export const Scanning: Story = {
  args: {
    value: "",
  },
  play: async ({ canvasElement }) => {
    await stubCameraAccess(true);
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Scan barcode" }));
  },
};

export const PermissionDenied: Story = {
  args: {
    value: "",
  },
  play: async ({ canvasElement }) => {
    await stubCameraAccess(false);
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole("button", { name: "Scan barcode" }));
  },
};

export const LookingUp: Story = {
  args: {
    value: "9781234567897",
    isLookingUp: true,
  },
};

export const WithError: Story = {
  args: {
    value: "",
    error: "Enter or scan a barcode",
  },
};

export const Disabled: Story = {
  args: {
    value: "9781234567897",
    disabled: true,
  },
};
