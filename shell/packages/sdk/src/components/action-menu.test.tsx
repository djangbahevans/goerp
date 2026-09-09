import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  describe("keyboard navigation (goerp#676)", () => {
    it("opening the menu moves focus to the first item", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Edit" }));
    });

    it("ArrowDown on the closed trigger opens the menu and focuses the first item", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      const trigger = screen.getByRole("button", { name: "Actions" });
      expect(screen.queryByRole("menu")).toBeNull();
      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Edit" }));
    });

    it("ArrowUp on the closed trigger opens the menu and focuses the last item", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      const trigger = screen.getByRole("button", { name: "Actions" });
      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Archive" }));
    });

    it("a keyboard-focused item gets the same background token as hover, per its design doc", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      expect(screen.getByRole("menuitem", { name: "Edit" }).className).toContain("focus:bg-surface-hover");
    });

    it("a disabled item suppresses the keyboard-focus highlight, same as it suppresses hover", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn(), disabled: true },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      const className = screen.getByRole("menuitem", { name: "Archive" }).className;
      expect(className).toContain("aria-disabled:hover:bg-transparent");
      expect(className).toContain("aria-disabled:focus:bg-transparent");
    });

    it("a danger-variant item's severity tint has focus parity with hover", () => {
      render(
        withPermissions(
          [],
          <ActionMenu label="Actions" items={[{ label: "Delete", onClick: vi.fn(), variant: "danger" }]} />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      const className = screen.getByRole("menuitem", { name: "Delete" }).className;
      expect(className).toContain("data-[variant=danger]:hover:text-danger-hover");
      expect(className).toContain("data-[variant=danger]:focus:text-danger-hover");
    });

    it("ArrowDown/ArrowUp move roving focus between items, wrapping at the ends", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Duplicate", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      const menu = screen.getByRole("menu");
      const [edit, duplicate, archive] = ["Edit", "Duplicate", "Archive"].map((name) =>
        screen.getByRole("menuitem", { name }),
      );

      fireEvent.keyDown(menu, { key: "ArrowDown" });
      expect(document.activeElement).toBe(duplicate);
      fireEvent.keyDown(menu, { key: "ArrowDown" });
      expect(document.activeElement).toBe(archive);
      // Wraps past the last item back to the first.
      fireEvent.keyDown(menu, { key: "ArrowDown" });
      expect(document.activeElement).toBe(edit);
      // Wraps the other direction past the first item back to the last.
      fireEvent.keyDown(menu, { key: "ArrowUp" });
      expect(document.activeElement).toBe(archive);
    });

    it("ArrowDown skips separators", () => {
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
      fireEvent.keyDown(screen.getByRole("menu"), { key: "ArrowDown" });
      expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Archive" }));
    });

    it("ArrowDown skips a permission-denied item, same as a separator", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn(), permission: "contacts:contact:delete" },
              { label: "Duplicate", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      fireEvent.keyDown(screen.getByRole("menu"), { key: "ArrowDown" });
      expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "Duplicate" }));
    });

    it("a disabled item is reachable by ArrowDown but activating it is a no-op", () => {
      // A real <button>'s Enter/Space keypress dispatches this same click
      // event via the browser's own default action — jsdom doesn't
      // simulate that mapping, so this exercises the identical onClick
      // guard a keyboard activation would hit.
      const onClick = vi.fn();
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick, disabled: true },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      const menu = screen.getByRole("menu");
      fireEvent.keyDown(menu, { key: "ArrowDown" });
      const archive = screen.getByRole("menuitem", { name: "Archive" });
      expect(document.activeElement).toBe(archive);
      expect(archive.getAttribute("aria-disabled")).toBe("true");
      fireEvent.click(archive);
      expect(onClick).not.toHaveBeenCalled();
      // Still open and still focused — a disabled item's activation is a
      // no-op, not a close.
      expect(screen.getByRole("menu")).toBeTruthy();
    });

    it("Escape closes the menu and returns focus to the trigger", () => {
      render(withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick: vi.fn() }]} />));
      const trigger = screen.getByRole("button", { name: "Actions" });
      fireEvent.click(trigger);
      fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
      expect(screen.queryByRole("menu")).toBeNull();
      expect(document.activeElement).toBe(trigger);
    });

    it("Escape doesn't propagate to close an ancestor's own Escape handler", () => {
      const ancestorEscape = vi.fn();
      render(
        // role="dialog": stands in for a real ancestor dialog's own
        // Escape-to-close listener.
        <div role="dialog" onKeyDown={ancestorEscape}>
          {withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick: vi.fn() }]} />)}
        </div>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
      expect(ancestorEscape).not.toHaveBeenCalled();
    });

    it("Tab closes the menu after letting focus move on, rather than staying stuck open", async () => {
      render(withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick: vi.fn() }]} />));
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      fireEvent.keyDown(screen.getByRole("menu"), { key: "Tab" });
      expect(screen.getByRole("menu")).toBeTruthy();
      await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    });

    it("selecting an item returns focus to the trigger", () => {
      render(withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick: vi.fn() }]} />));
      const trigger = screen.getByRole("button", { name: "Actions" });
      fireEvent.click(trigger);
      fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
      expect(document.activeElement).toBe(trigger);
    });

    it("only the currently active item is in the Tab order; the rest are removed via tabIndex", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Actions"
            items={[
              { label: "Edit", onClick: vi.fn() },
              { label: "Archive", onClick: vi.fn() },
            ]}
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      expect(screen.getByRole("menuitem", { name: "Edit" }).tabIndex).toBe(0);
      expect(screen.getByRole("menuitem", { name: "Archive" }).tabIndex).toBe(-1);
      fireEvent.keyDown(screen.getByRole("menu"), { key: "ArrowDown" });
      expect(screen.getByRole("menuitem", { name: "Edit" }).tabIndex).toBe(-1);
      expect(screen.getByRole("menuitem", { name: "Archive" }).tabIndex).toBe(0);
    });
  });

  describe("checked items (chrome-header.md's UserMenu theme toggle)", () => {
    it("renders a checkable item as menuitemcheckbox with the right aria-checked", () => {
      render(
        withPermissions(
          [],
          <ActionMenu label="Actions" items={[{ label: "Dark mode", onClick: vi.fn(), checked: true }]} />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      const item = screen.getByRole("menuitemcheckbox", { name: "Dark mode" });
      expect(item.getAttribute("aria-checked")).toBe("true");
    });

    it("an unchecked checkable item has aria-checked false and no visible checkmark", () => {
      render(
        withPermissions(
          [],
          <ActionMenu label="Actions" items={[{ label: "Dark mode", onClick: vi.fn(), checked: false }]} />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      const item = screen.getByRole("menuitemcheckbox", { name: "Dark mode" });
      expect(item.getAttribute("aria-checked")).toBe("false");
    });

    it("an item with no checked field stays a plain menuitem", () => {
      render(withPermissions([], <ActionMenu label="Actions" items={[{ label: "Edit", onClick: vi.fn() }]} />));
      fireEvent.click(screen.getByRole("button", { name: "Actions" }));
      expect(screen.getByRole("menuitem", { name: "Edit" })).toBeTruthy();
      expect(screen.queryByRole("menuitemcheckbox")).toBeNull();
    });
  });

  describe("custom trigger", () => {
    it("renders the caller's trigger instead of the default button, wired to the same open state", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Account"
            items={[{ label: "Sign out", onClick: vi.fn() }]}
            trigger={({ ref, open, onClick, onKeyDown }) => (
              <button ref={ref} type="button" aria-label="Account menu" onClick={onClick} onKeyDown={onKeyDown}>
                {open ? "open" : "closed"}
              </button>
            )}
          />,
        ),
      );
      expect(screen.queryByRole("button", { name: "Account" })).toBeNull();
      const trigger = screen.getByRole("button", { name: "Account menu" });
      expect(trigger.textContent).toBe("closed");
      fireEvent.click(trigger);
      expect(trigger.textContent).toBe("open");
      expect(screen.getByRole("menuitem", { name: "Sign out" })).toBeTruthy();
    });

    it("disabled={true} still blocks a custom trigger's click and ArrowDown, even one that ignores the disabled prop it's handed", () => {
      render(
        withPermissions(
          [],
          <ActionMenu
            label="Account"
            disabled
            items={[{ label: "Sign out", onClick: vi.fn() }]}
            trigger={({ ref, open, onClick, onKeyDown }) => (
              // Deliberately never applies `disabled` itself — the point
              // of this test is that ActionMenu doesn't rely on it doing so.
              <button ref={ref} type="button" aria-label="Account menu" onClick={onClick} onKeyDown={onKeyDown}>
                {open ? "open" : "closed"}
              </button>
            )}
          />,
        ),
      );
      const trigger = screen.getByRole("button", { name: "Account menu" });

      fireEvent.click(trigger);
      expect(trigger.textContent).toBe("closed");
      expect(screen.queryByRole("menuitem", { name: "Sign out" })).toBeNull();

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(trigger.textContent).toBe("closed");
    });
  });
});
