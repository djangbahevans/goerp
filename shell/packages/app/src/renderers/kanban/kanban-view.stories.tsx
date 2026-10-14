import { UserAvatar } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { useKanbanCard } from "@goerp/sdk/react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ReactNode } from "react";
import { expect, userEvent, within } from "storybook/test";
import { KanbanBoard } from "./kanban-board.js";
import type { KanbanGroup } from "./kanban-view-types.js";

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
    { id: "won", label: "Won", color: "#10B981", cards: [] },
  ];
}

function CRMLeadCard({ title, revenue }: { title: string; revenue: string }): ReactNode {
  const { isDragging, isDropTarget, columnId, onDragStart } = useKanbanCard();
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the enclosing card wrapper supplies keyboard drag controls.
    <div
      draggable
      onDragStart={onDragStart}
      data-testid={title}
      data-column={columnId}
      data-drop-target={isDropTarget}
      className={`${isDragging ? "opacity-50" : ""} ${isDropTarget ? "bg-primary-subtle" : ""}`}
    >
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
    {
      id: "won",
      label: "Won",
      cards: [
        {
          id: "lead-2",
          title: "Globex Inc",
          accessibleTitle: "Globex Inc",
          render: () => <CRMLeadCard title="Globex Inc" revenue="$12,000" />,
        },
      ],
    },
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
    const card = canvas.getByRole("button", { name: "Acme Corp" });
    const source = canvas.getByTestId("Acme Corp");
    const target = canvas.getByTestId("Globex Inc");
    await expect(source).toHaveAttribute("data-column", "new");
    await expect(target).toHaveAttribute("data-drop-target", "false");

    card.focus();
    await userEvent.keyboard(" ");
    await expect(source).toHaveAttribute("data-drop-target", "true");
    await userEvent.keyboard("{ArrowRight}");
    await expect(source).toHaveAttribute("data-column", "new");
    await expect(source).toHaveAttribute("data-drop-target", "false");
    await expect(target).toHaveAttribute("data-drop-target", "true");
    await userEvent.keyboard("{Escape}");
    await expect(target).toHaveAttribute("data-drop-target", "false");

    await userEvent.keyboard(" {ArrowRight} ");
    await expect(canvas.getByTestId("Acme Corp")).toHaveAttribute("data-column", "won");
    await expect(target).toHaveAttribute("data-drop-target", "false");
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

export const QuickCreateRejected: Story = {
  name: "Quick create rejected by the server",
  args: {
    quickCreate: true,
    quickCreateFields: [
      { name: "title", label: "Title", required: true },
      { name: "revenue", label: "Revenue" },
    ],
    onQuickCreate: async () => {
      throw new AppError({
        code: "validation_failed",
        message: "invalid",
        httpStatus: 422,
        fieldErrors: { revenue: ["Must be a number."] },
      });
    },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
    await userEvent.click(canvas.getAllByRole("button", { name: "Add" })[0] as HTMLElement);
    await expect(canvas.getByText("This field is required.")).toBeInTheDocument();
    await userEvent.type(canvas.getAllByPlaceholderText("Title")[0] as HTMLElement, "Globex");
    await userEvent.type(canvas.getAllByPlaceholderText("Revenue")[0] as HTMLElement, "lots");
    await userEvent.click(canvas.getAllByRole("button", { name: "Add" })[0] as HTMLElement);
    await expect(await canvas.findByText("Must be a number.")).toBeInTheDocument();
    await expect(canvas.getAllByPlaceholderText("Title")[0]).toHaveValue("Globex");
  },
};
