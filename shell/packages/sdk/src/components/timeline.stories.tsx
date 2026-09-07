import type { Meta, StoryObj } from "@storybook/react-vite";
import { Timeline, TimelineItem } from "./timeline.js";

const meta: Meta<typeof Timeline> = {
  title: "Data Display/Timeline",
  component: Timeline,
};

export default meta;

type Story = StoryObj<typeof Timeline>;

export const Default: Story = {
  render: () => (
    <Timeline>
      <TimelineItem
        title="Order confirmed"
        description="Order #1024 was confirmed by Ama Boateng"
        timestamp="2026-03-05T10:30:00.000Z"
        user={{ name: "Ama Boateng" }}
      />
      <TimelineItem
        title="Payment received"
        description="GHS 1,200.00 received via mobile money"
        timestamp="2026-03-05T09:15:00.000Z"
      />
      <TimelineItem title="Order placed" timestamp="2026-03-05T08:00:00.000Z" user={{ name: "Kwame Mensah" }} />
    </Timeline>
  ),
};
