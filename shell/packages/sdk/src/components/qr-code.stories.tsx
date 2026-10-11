import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { QRCode } from "./qr-code.js";

const meta: Meta<typeof QRCode> = {
  title: "Data Display/QRCode",
  component: QRCode,
  args: { value: "https://example.test/records/123", label: "Record" },
};

export default meta;
type Story = StoryObj<typeof QRCode>;

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByRole("img", { name: "QR code for Record" })).toBeVisible();
    await expect(canvas.getByText("https://example.test/records/123")).toBeVisible();
  },
};

export const Unicode: Story = { args: { value: "Akwaaba — 中文 😀 e\u0301" } };

export const Empty: Story = {
  args: { value: "" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.queryByRole("img")).not.toBeInTheDocument();
    await expect(canvas.getByText("No value")).toBeInTheDocument();
  },
};

export const Unencodable: Story = {
  args: { value: "x".repeat(2400) },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.queryByRole("img")).not.toBeInTheDocument();
    await expect(canvas.getByText("This value cannot be displayed as a QR code.")).toBeVisible();
    await expect(canvas.getByText("x".repeat(2400))).toBeVisible();
  },
};

export const NarrowColumn: Story = {
  args: { value: `https://example.test/${"a".repeat(160)}` },
  decorators: [
    (Story) => (
      <div style={{ width: 160 }}>
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    const code = within(canvasElement).getByRole("img");
    const bounds = code.getBoundingClientRect();
    await expect(bounds.width).toBeGreaterThan(0);
    await expect(bounds.width).toBeLessThanOrEqual(160);
    await expect(bounds.height).toBeCloseTo(bounds.width);
    const figure = code.closest("figure");
    await expect(figure?.scrollWidth).toBeLessThanOrEqual(160);
  },
};
