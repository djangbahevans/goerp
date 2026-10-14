import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { DragEvent } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { KanbanCardProvider, useKanbanCard } from "./use-kanban-card.js";

function Probe() {
  const { isDragging, isDropTarget, columnId, onDragStart, actions } = useKanbanCard();
  return (
    <button type="button" draggable onDragStart={onDragStart} data-testid="card">
      {String(isDragging)} / {String(isDropTarget)} / {columnId} / {actions.length} actions
    </button>
  );
}

afterEach(cleanup);

describe("useKanbanCard", () => {
  it("returns inactive state and a safe drag handler outside a provider", () => {
    render(<Probe />);
    expect(screen.getByTestId("card").textContent).toBe("false / false /  / 0 actions");
    fireEvent.dragStart(screen.getByTestId("card"));
  });

  it("exposes provider values and forwards drag events from a custom element", () => {
    const onDragStart = vi.fn<(event: DragEvent) => void>();
    render(
      <KanbanCardProvider
        isDragging
        isDropTarget
        columnId="won"
        onDragStart={onDragStart}
        actions={[{ label: "Mark Won", onClick: () => {} }]}
      >
        <Probe />
      </KanbanCardProvider>,
    );
    const card = screen.getByTestId("card");
    expect(card.textContent).toBe("true / true / won / 1 actions");

    const dataTransfer = { setData: vi.fn() };
    fireEvent.dragStart(card, { dataTransfer });
    expect(onDragStart).toHaveBeenCalledOnce();
    expect(onDragStart.mock.calls[0]?.[0].dataTransfer).toBe(dataTransfer);
  });

  it("updates the context when the card's state and column change", () => {
    const onDragStart = vi.fn();
    const { rerender } = render(
      <KanbanCardProvider isDragging={false} isDropTarget={false} columnId="new" onDragStart={onDragStart}>
        <Probe />
      </KanbanCardProvider>,
    );
    expect(screen.getByTestId("card").textContent).toBe("false / false / new / 0 actions");

    rerender(
      <KanbanCardProvider isDragging isDropTarget columnId="won" onDragStart={onDragStart}>
        <Probe />
      </KanbanCardProvider>,
    );
    expect(screen.getByTestId("card").textContent).toBe("true / true / won / 0 actions");
  });
});
