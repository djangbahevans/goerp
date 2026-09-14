import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { KanbanCardProvider, useKanbanCard } from "./use-kanban-card.js";

function Probe() {
  const { isDragging, actions } = useKanbanCard();
  return (
    <span>
      {isDragging ? "dragging" : "not dragging"} / {actions.length} actions
    </span>
  );
}

describe("useKanbanCard", () => {
  it("defaults to no actions outside a provider", () => {
    render(<Probe />);
    expect(screen.getByText("not dragging / 0 actions")).toBeTruthy();
  });

  it("exposes the actions passed to KanbanCardProvider, so a card_component override can still reach card_actions", () => {
    render(
      <KanbanCardProvider isDragging actions={[{ label: "Mark Won", onClick: () => {} }]}>
        <Probe />
      </KanbanCardProvider>,
    );
    expect(screen.getByText("dragging / 1 actions")).toBeTruthy();
  });
});
