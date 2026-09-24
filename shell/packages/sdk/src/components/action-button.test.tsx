import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createPermissionContextValue, PermissionContext } from "../auth/permission-provider.js";
import { ActionButton } from "./action-button.js";
import { Button } from "./button.js";

afterEach(cleanup);

function withPermissions(permissions: string[], children: ReactNode) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

describe("ActionButton", () => {
  it("renders and fires onClick when no permission is required", () => {
    const onClick = vi.fn();
    render(withPermissions([], <ActionButton onClick={onClick}>Confirm Order</ActionButton>));
    fireEvent.click(screen.getByRole("button", { name: "Confirm Order" }));
    expect(onClick).toHaveBeenCalled();
  });

  it("renders when the current user has the required permission", () => {
    render(
      withPermissions(
        ["sales:order:confirm"],
        <ActionButton permission="sales:order:confirm" onClick={vi.fn()}>
          Confirm Order
        </ActionButton>,
      ),
    );
    expect(screen.getByRole("button", { name: "Confirm Order" })).toBeTruthy();
  });

  it("hides itself entirely when the current user lacks the required permission", () => {
    render(
      withPermissions(
        [],
        <ActionButton permission="sales:order:confirm" onClick={vi.fn()}>
          Confirm Order
        </ActionButton>,
      ),
    );
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("blocks clicks while loading without setting the native disabled attribute", () => {
    const onClick = vi.fn();
    render(
      withPermissions(
        [],
        <ActionButton onClick={onClick} loading>
          Confirm Order
        </ActionButton>,
      ),
    );
    const button = screen.getByRole("button");
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
    expect(button.hasAttribute("disabled")).toBe(false);
    expect(button.getAttribute("aria-busy")).toBe("true");
  });

  it("renders the same classes as a Button with the same variant", () => {
    render(
      withPermissions(
        [],
        <>
          <ActionButton onClick={vi.fn()} variant="danger">
            Archive
          </ActionButton>
          <Button variant="danger">Archive</Button>
        </>,
      ),
    );
    const [action, plain] = screen.getAllByRole("button", { name: "Archive" });
    expect(action?.className).toBe(plain?.className);
  });

  it("defaults to the md size", () => {
    render(withPermissions([], <ActionButton onClick={vi.fn()}>Confirm Order</ActionButton>));
    expect(screen.getByRole("button").getAttribute("data-size")).toBe("md");
  });

  it("applies a sm size", () => {
    render(
      withPermissions(
        [],
        <ActionButton onClick={vi.fn()} size="sm">
          Confirm Order
        </ActionButton>,
      ),
    );
    expect(screen.getByRole("button").getAttribute("data-size")).toBe("sm");
  });

  it("throws when rendered outside a PermissionProvider", () => {
    expect(() => render(<ActionButton onClick={vi.fn()}>Confirm Order</ActionButton>)).toThrow(
      /must be used within a PermissionProvider/,
    );
  });
});
