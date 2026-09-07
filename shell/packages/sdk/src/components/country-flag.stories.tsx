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

// "UK" is well-formatted (2 letters) but isn't the flag-icons/ISO 3166-1
// code for the United Kingdom ("GB") — falls back to the raw code as text
// rather than a blank or incorrect flag glyph.
export const InvalidCode: Story = {
  args: {
    code: "UK",
    showName: true,
  },
};
