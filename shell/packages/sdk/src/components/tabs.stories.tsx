import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { TabPanel, Tabs } from "./tabs.js";

const meta: Meta<typeof Tabs> = {
  title: "Navigation/Tabs",
  component: Tabs,
};

export default meta;

type Story = StoryObj<typeof Tabs>;

function TabsDemo() {
  const [activeId, setActiveId] = useState("details");
  return (
    <Tabs
      items={[
        { id: "details", label: "Details" },
        { id: "activity", label: "Activity", badge: 3 },
        { id: "financials", label: "Financials", disabled: true },
      ]}
      activeId={activeId}
      onChange={setActiveId}
    >
      <TabPanel id="details">
        <p>Name: Ama Boateng</p>
        <p>Email: ama@example.com</p>
      </TabPanel>
      <TabPanel id="activity">
        <p>Order confirmed — 2 hours ago</p>
      </TabPanel>
      <TabPanel id="financials">
        <p>Financials content</p>
      </TabPanel>
    </Tabs>
  );
}

export const Default: Story = {
  render: () => <TabsDemo />,
};
