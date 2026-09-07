import type { Meta, StoryObj } from "@storybook/react-vite";
import { CountryFlag } from "./country-flag.js";

const meta: Meta<typeof CountryFlag> = {
  title: "Badges & Indicators/CountryFlag",
  component: CountryFlag,
};

export default meta;

type Story = StoryObj<typeof CountryFlag>;

export const FlagOnly: Story = {
  args: {
    code: "GH",
  },
};

export const WithName: Story = {
  args: {
    code: "GH",
    showName: true,
  },
};
