import type { Meta, StoryObj } from "@storybook/react-vite";
import { BulkActionContext } from "../react/bulk-action-context.js";
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
    <BulkActionContext.Provider
      value={{
        selectedIds: ["1", "2", "3"],
        selectedCount: 3,
        onComplete: () => {},
        onCancel: () => {},
        isLoading: false,
        setLoading: () => {},
      }}
    >
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
    </BulkActionContext.Provider>
  ),
};
