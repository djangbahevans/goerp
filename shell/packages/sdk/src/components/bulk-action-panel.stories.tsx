import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { BulkActionContext } from "../react/bulk-action-context.js";
import { ActionButton } from "./action-button.js";
import { BulkActionPanel } from "./bulk-action-panel.js";
import { Select } from "./select.js";

const TAG_OPTIONS = [
  { value: "vip", label: "VIP" },
  { value: "wholesale", label: "Wholesale" },
];

const meta: Meta<typeof BulkActionPanel> = {
  title: "Actions/BulkActionPanel",
  component: BulkActionPanel,
};

export default meta;

type Story = StoryObj<typeof BulkActionPanel>;

export const Default: Story = {
  render: function Render() {
    const [tag, setTag] = useState("vip");
    return (
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
          <div className="w-48">
            <Select options={TAG_OPTIONS} value={tag} onChange={(value) => setTag(value as string)} />
          </div>
          <ActionButton variant="primary" onClick={() => {}}>
            Add to 3 contacts
          </ActionButton>
          <ActionButton variant="ghost" onClick={() => {}}>
            Cancel
          </ActionButton>
        </BulkActionPanel>
      </BulkActionContext.Provider>
    );
  },
};
