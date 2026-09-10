import { toast } from "@goerp/sdk/notifications";
import { useKanbanCard } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { KanbanBoard } from "./kanban-board.js";
import type { KanbanGroup } from "./kanban-view-types.js";

afterEach(cleanup);

function makeGroups(): KanbanGroup[] {
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
          secondaryFields: [{ key: "revenue", value: "$10,000" }],
          avatar: { name: "Jane Doe" },
        },
        {
          id: "lead-2",
          title: "Globex Inc",
          accessibleTitle: "Globex Inc",
          secondaryFields: [{ key: "revenue", value: "$5,000" }],
        },
      ],
    },
    { id: "won", label: "Won", cards: [] },
  ];
}

describe("KanbanBoard", () => {
  it("renders each group's label, card count, and card content", () => {
    render(<KanbanBoard groups={makeGroups()} onMoveCard={vi.fn()} />);
    expect(screen.getByText("New")).toBeTruthy();
    expect(screen.getByText("Won")).toBeTruthy();
    expect(screen.getByText("Acme Corp")).toBeTruthy();
    expect(screen.getByText("$10,000")).toBeTruthy();
    expect(screen.getByText("JD")).toBeTruthy();
  });

  it("shows an inline empty state for a column with no cards", () => {
    render(<KanbanBoard groups={makeGroups()} onMoveCard={vi.fn()} />);
    expect(screen.getByText("No cards in this column.")).toBeTruthy();
  });

  it("uses a custom empty-column message when provided", () => {
    render(
      <KanbanBoard
        groups={makeGroups()}
        onMoveCard={vi.fn()}
        emptyColumnMessage={(group) => `No leads in ${group.label}`}
      />,
    );
    expect(screen.getByText("No leads in Won")).toBeTruthy();
  });

  it("moves a card between columns via the full keyboard sequence: pick up, arrow, drop", async () => {
    const onMoveCard = vi.fn().mockResolvedValue(undefined);
    render(<KanbanBoard groups={makeGroups()} onMoveCard={onMoveCard} />);
    const card = screen.getByRole("button", { name: "Acme Corp" });
    card.focus();

    fireEvent.keyDown(card, { key: " " });
    expect(screen.getByText(/picked up/)).toBeTruthy();

    fireEvent.keyDown(card, { key: "ArrowRight" });
    expect(screen.getByText(/Moved to Won/)).toBeTruthy();

    fireEvent.keyDown(card, { key: " " });
    expect(onMoveCard).toHaveBeenCalledWith("lead-1", "new", "won");
  });

  it("Escape cancels a keyboard pick-up without calling onMoveCard", () => {
    const onMoveCard = vi.fn();
    render(<KanbanBoard groups={makeGroups()} onMoveCard={onMoveCard} />);
    const card = screen.getByRole("button", { name: "Acme Corp" });
    card.focus();

    fireEvent.keyDown(card, { key: " " });
    fireEvent.keyDown(card, { key: "ArrowRight" });
    fireEvent.keyDown(card, { key: "Escape" });

    expect(onMoveCard).not.toHaveBeenCalled();
    expect(screen.getByText(/Move cancelled/)).toBeTruthy();
  });

  it("reverts the optimistic move and reports Toast.error when onMoveCard rejects", async () => {
    const toastSpy = vi.spyOn(toast, "error").mockImplementation(() => {});
    const onMoveCard = vi.fn().mockRejectedValue(new Error("network error"));
    render(<KanbanBoard groups={makeGroups()} onMoveCard={onMoveCard} />);
    const card = screen.getByRole("button", { name: "Acme Corp" });
    card.focus();

    fireEvent.keyDown(card, { key: " " });
    fireEvent.keyDown(card, { key: "ArrowRight" });
    fireEvent.keyDown(card, { key: " " });

    await vi.waitFor(() => expect(toastSpy).toHaveBeenCalled());
    expect(screen.getByText("No cards in this column.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Acme Corp" })).toBeTruthy();
  });

  it("moves a card between columns on a native mouse drag-and-drop", () => {
    const onMoveCard = vi.fn().mockResolvedValue(undefined);
    render(<KanbanBoard groups={makeGroups()} onMoveCard={onMoveCard} />);
    const card = screen.getByRole("button", { name: "Acme Corp" });
    const wonHeading = screen.getByText("Won");
    const wonColumn = wonHeading.parentElement?.parentElement;
    if (!wonColumn) throw new Error("Won column not found");

    const dataTransfer = { setData: vi.fn(), getData: vi.fn().mockReturnValue("lead-1"), effectAllowed: "" };
    fireEvent.dragStart(card, { dataTransfer });
    fireEvent.dragOver(wonColumn, { dataTransfer });
    fireEvent.drop(wonColumn, { dataTransfer });

    expect(onMoveCard).toHaveBeenCalledWith("lead-1", "new", "won");
  });

  it("reorders cards within the same column via a native drag, without calling onMoveCard", () => {
    const onMoveCard = vi.fn().mockResolvedValue(undefined);
    render(<KanbanBoard groups={makeGroups()} onMoveCard={onMoveCard} />);
    const dragged = screen.getByRole("button", { name: "Globex Inc" });
    const target = screen.getByRole("button", { name: "Acme Corp" });

    const dataTransfer = { setData: vi.fn(), getData: vi.fn().mockReturnValue("lead-2"), effectAllowed: "" };
    fireEvent.dragStart(dragged, { dataTransfer });
    // fireEvent's { clientY } option doesn't reach a jsdom DragEvent's own
    // .clientY (stays undefined), so it's set directly on a manually built
    // event instead. A negative value sits above the (jsdom-zeroed) target
    // rect's midpoint, i.e. its "before" half.
    function dragOverBefore(el: HTMLElement): void {
      const event = new Event("dragover", { bubbles: true, cancelable: true });
      Object.defineProperty(event, "clientY", { value: -1 });
      Object.defineProperty(event, "dataTransfer", { value: dataTransfer });
      fireEvent(el, event);
    }
    function dropBefore(el: HTMLElement): void {
      const event = new Event("drop", { bubbles: true, cancelable: true });
      Object.defineProperty(event, "clientY", { value: -1 });
      Object.defineProperty(event, "dataTransfer", { value: dataTransfer });
      fireEvent(el, event);
    }
    dragOverBefore(target);
    dropBefore(target);

    expect(onMoveCard).not.toHaveBeenCalled();
    const order = screen.getAllByRole("button").map((el) => el.getAttribute("aria-label"));
    expect(order.indexOf("Globex Inc")).toBeLessThan(order.indexOf("Acme Corp"));
  });

  it("renders a custom card_component override, which reads isDragging via useKanbanCard()", () => {
    function CustomCard(): ReactNode {
      const { isDragging } = useKanbanCard();
      return <span>{isDragging ? "dragging" : "not dragging"}</span>;
    }
    const groups = makeGroups();
    const firstCard = groups[0]?.cards[0];
    if (firstCard) firstCard.render = () => <CustomCard />;

    render(<KanbanBoard groups={groups} onMoveCard={vi.fn()} />);
    expect(screen.getByText("not dragging")).toBeTruthy();
  });

  it("opens the inline quick-create row and submits entered values", () => {
    const onQuickCreate = vi.fn();
    render(<KanbanBoard groups={makeGroups()} onMoveCard={vi.fn()} quickCreate onQuickCreate={onQuickCreate} />);

    fireEvent.click(screen.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
    const input = screen.getAllByPlaceholderText("Title")[0] as HTMLElement;
    fireEvent.change(input, { target: { value: "New Lead" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0] as HTMLElement);

    expect(onQuickCreate).toHaveBeenCalledWith("new", { title: "New Lead" });
  });
});
