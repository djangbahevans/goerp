import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IconButton } from "./icon-button.js";

afterEach(cleanup);

describe("IconButton", () => {
  it("names the button from label and fires onClick", () => {
    const onClick = vi.fn();
    render(<IconButton icon="x" label="Dismiss" onClick={onClick} />);
    const button = screen.getByRole("button", { name: "Dismiss" });
    expect(button.getAttribute("type")).toBe("button");
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("omits aria-pressed for a plain action", () => {
    render(<IconButton icon="x" label="Dismiss" />);
    expect(screen.getByRole("button").hasAttribute("aria-pressed")).toBe(false);
  });

  it("reflects pressed as aria-pressed and the selected fill", () => {
    render(<IconButton icon="bold" label="Bold" pressed />);
    const button = screen.getByRole("button", { name: "Bold" });
    expect(button.getAttribute("aria-pressed")).toBe("true");
    expect(button.className).toContain("bg-primary-subtle");
  });

  it("renders aria-pressed=false when not pressed", () => {
    render(<IconButton icon="bold" label="Bold" pressed={false} />);
    expect(screen.getByRole("button").getAttribute("aria-pressed")).toBe("false");
  });

  it("matches Button's heights", () => {
    render(
      <>
        <IconButton icon="x" label="Small" size="sm" />
        <IconButton icon="x" label="Medium" />
      </>,
    );
    expect(screen.getByRole("button", { name: "Small" }).className).toContain("size-7");
    expect(screen.getByRole("button", { name: "Medium" }).className).toContain("size-9");
  });

  it("disables with the native attribute and the dimmed look", () => {
    const onClick = vi.fn();
    render(<IconButton icon="x" label="Dismiss" disabled onClick={onClick} />);
    const button = screen.getByRole("button");
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
    expect(button.getAttribute("data-disabled")).toBe("true");
  });
});
