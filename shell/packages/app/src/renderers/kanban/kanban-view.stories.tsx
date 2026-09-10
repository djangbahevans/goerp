import { UserAvatar } from "@goerp/sdk/components";
import { useKanbanCard } from "@goerp/sdk/react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ReactNode } from "react";
import { expect, userEvent, within } from "storybook/test";
import { KanbanBoard } from "./kanban-board.js";
import type { KanbanGroup } from "./kanban-view-types.js";

// view-system.md §6's leads_kanban manifest example — card_fields:
// [avatar_file_id, display_name, expected_revenue, probability,
// assigned_to_name], already resolved into presentational props (goerp#644's
// concern, not this primitive's).
function sampleGroups(): KanbanGroup[] {
  return [
    {
      id: "new",
      label: "New",
      color: "#3B82F6",
      cards: [
        {
          id: "lead-1",
          title: "Acme Corp",
          accessibleTitle: "Acme Corp",
          avatar: { name: "Jane Doe", userId: "u1" },
          secondaryFields: [
            { key: "revenue", value: "$45,000" },
            { key: "probability", value: "20%" },
          ],
          actions: [
            { label: "Edit", onClick: () => {} },
            { label: "Mark Won", onClick: () => {} },
            { label: "Mark Lost", onClick: () => {}, variant: "danger" },
          ],
        },
        {
          id: "lead-2",
          title: "Globex Inc",
          accessibleTitle: "Globex Inc",
          avatar: { name: "Sam Lee", userId: "u2" },
          secondaryFields: [
            { key: "revenue", value: "$12,000" },
            { key: "probability", value: "10%" },
          ],
        },
      ],
    },
    {
      id: "qualified",
      label: "Qualified",
      color: "#8B5CF6",
      cards: [
        {
          id: "lead-3",
          title: "Initech",
          accessibleTitle: "Initech",
          avatar: { name: "Priya Nair", userId: "u3" },
          secondaryFields: [
            { key: "revenue", value: "$80,000" },
            { key: "probability", value: "60%" },
          ],
          actions: [{ label: "Edit", onClick: () => {} }],
        },
      ],
    },
    // Deliberately empty — exercises KanbanColumn's inline EmptyState.
    { id: "won", label: "Won", color: "#10B981", cards: [] },
  ];
}

// view-system.md §6's documented CRMLeadCard example: a card_component
// override consuming useKanbanCard() directly for its own isDragging
// treatment, rather than inheriting KanbanCard's generic opacity handling.
function CRMLeadCard({ title, revenue }: { title: string; revenue: string }): ReactNode {
  const { isDragging } = useKanbanCard();
  return (
    <div className={isDragging ? "opacity-50" : ""}>
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-sm">{title}</span>
      </div>
      <div className="mt-1 flex items-center gap-2">
        <UserAvatar name="Jane Doe" size="sm" />
        <span className="text-text-secondary text-xs">{revenue}</span>
      </div>
    </div>
  );
}

function customCardGroups(): KanbanGroup[] {
  return [
    {
      id: "new",
      label: "New",
      color: "#3B82F6",
      cards: [
        {
          id: "lead-1",
          title: "Acme Corp",
          accessibleTitle: "Acme Corp",
          render: () => <CRMLeadCard title="Acme Corp" revenue="$45,000" />,
        },
      ],
    },
    { id: "won", label: "Won", cards: [] },
  ];
}

const meta: Meta<typeof KanbanBoard> = {
  title: "Renderers/KanbanBoard",
  component: KanbanBoard,
  args: {
    groups: sampleGroups(),
    onMoveCard: async () => {},
  },
};

export default meta;

type Story = StoryObj<typeof KanbanBoard>;

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Acme Corp")).toBeInTheDocument();
    await expect(canvas.getByText("No cards in this column.")).toBeInTheDocument();
  },
};

export const DraggingCard: Story = {
  name: "Card picked up via keyboard",
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const card = canvas.getByRole("button", { name: "Acme Corp" });
    card.focus();
    await userEvent.keyboard(" ");
    await expect(canvas.getByText(/picked up/)).toBeInTheDocument();
    await expect(card.className).toContain("border-dashed");
  },
};

export const CustomCardComponent: Story = {
  name: "Custom card_component override",
  args: { groups: customCardGroups() },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("$45,000")).toBeInTheDocument();
  },
};

export const QuickCreate: Story = {
  name: "Inline quick_create row",
  args: { quickCreate: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const addButtons = canvas.getAllByRole("button", { name: "+ Add" });
    await userEvent.click(addButtons[0] as HTMLElement);
    await expect(canvas.getAllByPlaceholderText("Title")[0]).toBeInTheDocument();
  },
};
