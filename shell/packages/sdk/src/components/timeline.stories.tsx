import type { Meta, StoryObj } from "@storybook/react-vite";
import { Icon } from "./icon.js";
import { IconButton } from "./icon-button.js";
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

export const WithChildrenAndActions: Story = {
  render: () => (
    <Timeline>
      <TimelineItem
        icon={<Icon name="message-square" size={16} />}
        title="Commented"
        timestamp="2026-03-05T10:30:00.000Z"
        user={{ name: "Ama Boateng" }}
        actions={<IconButton icon="trash-2" label="Delete comment" variant="danger" size="sm" />}
      >
        <p className="whitespace-pre-wrap text-sm text-text">
          {"Customer asked to move delivery to Friday.\nConfirmed by phone."}
        </p>
      </TimelineItem>
      <TimelineItem
        icon={<Icon name="pencil" size={16} />}
        title="Changed 2 fields"
        timestamp="2026-03-05T09:15:00.000Z"
        user={{ name: "Kwame Mensah" }}
      >
        <ul className="space-y-1 text-sm text-text-secondary">
          <li>
            Status: Draft → <span className="text-text">Confirmed</span>
          </li>
          <li>
            Delivery On: Mar 6, 2026 → <span className="text-text">Mar 8, 2026</span>
          </li>
        </ul>
      </TimelineItem>
    </Timeline>
  ),
};
