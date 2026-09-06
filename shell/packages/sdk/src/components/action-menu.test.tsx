import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createPermissionContextValue, PermissionContext } from "../auth/permission-provider.js";
import { ActionMenu } from "./action-menu.js";

afterEach(cleanup);

function withPermissions(permissions: string[], children: ReactNode) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

describe("ActionMenu", () => {
  it("is closed until the trigger is clicked", () => {
    render(withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick: vi.fn() }]} />));
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    expect(screen.getByRole("menu")).toBeTruthy();
  });

  it("calls the item's onClick and closes the menu", () => {
    const onClick = vi.fn();
    render(withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick }]} />));
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(onClick).toHaveBeenCalled();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("renders a separator between items", () => {
    render(
      withPermissions(
        [],
        <ActionMenu
          label="Actions"
          items={[{ label: "Edit", onClick: vi.fn() }, { type: "separator" }, { label: "Archive", onClick: vi.fn() }]}
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    expect(screen.getByRole("separator")).toBeTruthy();
  });

  it("hides an item the current user lacks permission for", () => {
    render(
      withPermissions(
        [],
        <ActionMenu
          label="Actions"
          items={[
            { label: "Edit", onClick: vi.fn() },
            { label: "Archive", onClick: vi.fn(), permission: "contacts:contact:delete", variant: "danger" },
          ]}
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    expect(screen.getByRole("menuitem", { name: "Edit" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Archive" })).toBeNull();
  });

  it("shows a permission-gated item once the current user has it", () => {
    render(
      withPermissions(
        ["contacts:contact:delete"],
        <ActionMenu
          label="Actions"
          items={[{ label: "Archive", onClick: vi.fn(), permission: "contacts:contact:delete" }]}
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    expect(screen.getByRole("menuitem", { name: "Archive" })).toBeTruthy();
  });
});
