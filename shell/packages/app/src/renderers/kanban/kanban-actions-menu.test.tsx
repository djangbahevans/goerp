import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { ActionMenuItem } from "@goerp/sdk/components";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { KanbanActionsMenu } from "./kanban-actions-menu.js";

afterEach(cleanup);

function withPermissions(children: ReactNode) {
  const value = createPermissionContextValue({ permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set() });
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

describe("KanbanActionsMenu", () => {
  it("renders a single action as a compact button and fires it directly", () => {
    const onClick = vi.fn();
    const actions: ActionMenuItem[] = [{ label: "Mark Won", onClick }];
    render(withPermissions(<KanbanActionsMenu actions={actions} label="Card actions" />));

    fireEvent.click(screen.getByRole("button", { name: "Mark Won" }));
    expect(onClick).toHaveBeenCalled();
  });

  it("a confirm-gated single action skips the compact button and renders a real menu instead", () => {
    const onClick = vi.fn();
    const actions: ActionMenuItem[] = [{ label: "Archive", onClick, confirm: { title: "Archive?", message: "Sure?" } }];
    render(withPermissions(<KanbanActionsMenu actions={actions} label="Card actions" />));

    // Not the compact single-button shortcut — an accessible menu trigger instead.
    expect(screen.queryByRole("button", { name: "Archive" })).toBeNull();
    const trigger = screen.getByRole("button", { name: "Card actions" });
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("menuitem", { name: "Archive" }));

    expect(onClick).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Archive?" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(onClick).toHaveBeenCalled();
  });
});
