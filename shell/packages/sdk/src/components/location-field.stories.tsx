import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { LocationField, type LocationValue } from "./location-field.js";

const meta: Meta<typeof LocationField> = {
  title: "Form Fields/LocationField",
  component: LocationField,
  args: {
    onChange: () => {},
  },
};

export default meta;

type Story = StoryObj<typeof LocationField>;

// No tileUrl configured in any story — matches the field-renderers.tsx
// wiring today (docs/components/location-field.md's Open Questions: where
// a real deployment's PMTiles archive URL comes from is still unresolved),
// so every story renders the plain-background fallback rather than a real
// basemap. Click/drag/coordinate-input behavior is identical either way.
function LocationFieldDemo() {
  const [value, setValue] = useState<Partial<LocationValue> | undefined>(undefined);
  return <LocationField label="Site location" value={value} onChange={setValue} />;
}

export const Empty: Story = {
  render: () => <LocationFieldDemo />,
};

export const Default: Story = {
  args: {
    label: "Site location",
    value: { lat: 5.56, lng: -0.2057 },
  },
};

export const WithError: Story = {
  args: {
    label: "Site location",
    value: { lat: 5.56, lng: -0.2057 },
    error: "Location is required",
  },
};

export const Disabled: Story = {
  args: {
    label: "Site location",
    value: { lat: 5.56, lng: -0.2057 },
    disabled: true,
  },
};
