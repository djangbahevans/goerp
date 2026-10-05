import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { useKanbanCard } from "@goerp/sdk/react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { KanbanBoard } from "./kanban-board.js";
import type { KanbanBoardProps, KanbanGroup, KanbanQuickCreateField } from "./kanban-view-types.js";

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

  it("outlines a group's color swatch so an arbitrary tenant color can't exactly match the column background", () => {
    const { container } = render(<KanbanBoard groups={makeGroups()} onMoveCard={vi.fn()} />);
    const swatch = container.querySelector('[aria-hidden="true"].rounded-full');
    expect(swatch?.className).toContain("border-border");
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

  it("still exposes card_actions to a custom card_component override via useKanbanCard()", () => {
    const onMarkWon = vi.fn();
    function CustomCard(): ReactNode {
      const { actions } = useKanbanCard();
      return (
        <>
          {actions.map((action) => (
            <button key={action.label} type="button" onClick={() => action.onClick?.()}>
              {action.label}
            </button>
          ))}
        </>
      );
    }
    const groups = makeGroups();
    const firstCard = groups[0]?.cards[0];
    if (firstCard) {
      firstCard.render = () => <CustomCard />;
      firstCard.actions = [{ label: "Mark Won", onClick: onMarkWon }];
    }

    render(<KanbanBoard groups={groups} onMoveCard={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Mark Won" }));
    expect(onMarkWon).toHaveBeenCalled();
  });

  it("opens the inline quick-create row and submits entered values", () => {
    const onQuickCreate = vi.fn().mockResolvedValue(undefined);
    render(<KanbanBoard groups={makeGroups()} onMoveCard={vi.fn()} quickCreate onQuickCreate={onQuickCreate} />);

    fireEvent.click(screen.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
    const input = screen.getAllByPlaceholderText("Title")[0] as HTMLElement;
    fireEvent.change(input, { target: { value: "New Lead" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0] as HTMLElement);

    expect(onQuickCreate).toHaveBeenCalledWith("new", { title: "New Lead" });
  });

  describe("quick create validation", () => {
    function openRow(
      onQuickCreate: KanbanBoardProps["onQuickCreate"],
      fields: KanbanQuickCreateField[] = [{ name: "title", label: "Title", required: true }],
    ) {
      render(
        <KanbanBoard
          groups={makeGroups()}
          onMoveCard={vi.fn()}
          quickCreate
          quickCreateFields={fields}
          onQuickCreate={onQuickCreate}
        />,
      );
      fireEvent.click(screen.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
    }
    const submit = () => fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0] as HTMLElement);

    it("blocks an empty required field and names it without sending", () => {
      const onQuickCreate = vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>();
      openRow(onQuickCreate);
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "   " } });
      submit();

      expect(onQuickCreate).not.toHaveBeenCalled();
      expect(screen.getByRole("alert").textContent).toBe("This field is required.");
      expect(screen.getByPlaceholderText("Title").getAttribute("aria-invalid")).toBe("true");
      expect(document.activeElement).toBe(screen.getByPlaceholderText("Title"));
    });

    it("focuses the first empty required field, not the first field", () => {
      openRow(vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>(), [
        { name: "title", label: "Title", required: true },
        { name: "revenue", label: "Revenue", required: true },
      ]);
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "Lead" } });
      submit();
      expect(document.activeElement).toBe(screen.getByPlaceholderText("Revenue"));
    });

    it("shows the error message when a 422 carries no field errors", async () => {
      const err = new AppError({
        code: "validation_failed",
        message: "Invalid lead",
        httpStatus: 422,
        fieldErrors: {},
      });
      openRow(vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>().mockRejectedValue(err));
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "x" } });
      submit();
      expect((await screen.findByRole("alert")).textContent).toBe("Invalid lead");
    });

    it("does not require a field the model does not mark required", () => {
      const onQuickCreate = vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>().mockResolvedValue(undefined);
      openRow(onQuickCreate, [{ name: "title", label: "Title" }]);
      submit();
      expect(onQuickCreate).toHaveBeenCalledWith("new", {});
    });

    it("clears a field's error when it is edited", () => {
      openRow(vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>());
      submit();
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "x" } });
      expect(screen.queryByRole("alert")).toBeNull();
    });

    it("makes inputs read-only and the submit busy while in flight, then clears and closes on success", async () => {
      let resolve: () => void = () => {};
      const onQuickCreate = vi.fn(() => new Promise<void>((r) => (resolve = r)));
      openRow(onQuickCreate);
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "Lead" } });
      submit();

      await waitFor(() => expect(screen.getByPlaceholderText("Title").hasAttribute("readonly")).toBe(true));
      expect(screen.getAllByRole("button", { name: "Add" })[0]?.getAttribute("aria-busy")).toBe("true");
      await act(async () => resolve());

      await waitFor(() => expect(screen.queryByPlaceholderText("Title")).toBeNull());
      fireEvent.click(screen.getAllByRole("button", { name: "+ Add" })[0] as HTMLElement);
      expect((screen.getByPlaceholderText("Title") as HTMLInputElement).value).toBe("");
    });

    it("keeps the values and shows 422 field errors on their fields", async () => {
      const err = new AppError({
        code: "validation_failed",
        message: "invalid",
        httpStatus: 422,
        fieldErrors: { title: ["Too short.", "Must be unique."] },
      });
      openRow(vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>().mockRejectedValue(err));
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "x" } });
      submit();

      expect((await screen.findByRole("alert")).textContent).toBe("Too short. Must be unique.");
      const input = screen.getByPlaceholderText("Title") as HTMLInputElement;
      expect(input.value).toBe("x");
      expect(input.hasAttribute("readonly")).toBe(false);
      expect(input.getAttribute("aria-describedby")).toBe(screen.getByRole("alert").id);
    });

    it("shows a 422 error for a field the row lacks in the alert line", async () => {
      const err = new AppError({
        code: "validation_failed",
        message: "invalid",
        httpStatus: 422,
        fieldErrors: { owner_id: ["is required"] },
      });
      openRow(vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>().mockRejectedValue(err));
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "x" } });
      submit();

      expect((await screen.findByRole("alert")).textContent).toBe("owner_id: is required");
    });

    it("shows any other failure as an alert line and keeps the values", async () => {
      openRow(
        vi.fn<NonNullable<KanbanBoardProps["onQuickCreate"]>>().mockRejectedValue(new Error("Server unavailable")),
      );
      fireEvent.change(screen.getByPlaceholderText("Title"), { target: { value: "x" } });
      submit();

      expect((await screen.findByRole("alert")).textContent).toBe("Server unavailable");
      expect((screen.getByPlaceholderText("Title") as HTMLInputElement).value).toBe("x");
    });
  });

  it("disables both keyboard pick-up and native drag when allowDrag is false", () => {
    const onMoveCard = vi.fn();
    render(<KanbanBoard groups={makeGroups()} onMoveCard={onMoveCard} allowDrag={false} />);
    const card = screen.getByRole("button", { name: "Acme Corp" });
    expect(card).toHaveProperty("draggable", false);

    card.focus();
    fireEvent.keyDown(card, { key: " " });
    expect(screen.queryByText(/picked up/)).toBeNull();

    const dataTransfer = { setData: vi.fn(), getData: vi.fn().mockReturnValue("lead-1"), effectAllowed: "" };
    fireEvent.dragStart(card, { dataTransfer });
    expect(onMoveCard).not.toHaveBeenCalled();
  });

  it("renders a column's actions as an overflow menu in its header", () => {
    const onEdit = vi.fn();
    const groups = makeGroups();
    const won = groups.find((g) => g.id === "won");
    if (won)
      won.actions = [
        { label: "Rename", onClick: onEdit },
        { label: "Archive", onClick: vi.fn() },
      ];
    const permissionValue = createPermissionContextValue({
      permissions: new Set(),
      fieldAccess: {},
      modulesEnabled: new Set(),
    });

    render(
      <PermissionContext.Provider value={permissionValue}>
        <KanbanBoard groups={groups} onMoveCard={vi.fn()} />
      </PermissionContext.Provider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Column actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
    expect(onEdit).toHaveBeenCalled();
  });
});
