import type { Meta, StoryObj } from "@storybook/react-vite";
import { ActionButton } from "./action-button.js";
import { BulkActionPanel } from "./bulk-action-panel.js";

const meta: Meta<typeof BulkActionPanel> = {
  title: "Actions/BulkActionPanel",
  component: BulkActionPanel,
};

export default meta;

type Story = StoryObj<typeof BulkActionPanel>;

export const Default: Story = {
  render: () => (
    <BulkActionPanel>
      <select>
        <option>VIP</option>
        <option>Wholesale</option>
      </select>
      <ActionButton variant="primary" onClick={() => {}}>
        Add to 3 contacts
      </ActionButton>
      <ActionButton variant="ghost" onClick={() => {}}>
        Cancel
      </ActionButton>
    </BulkActionPanel>
  ),
};
