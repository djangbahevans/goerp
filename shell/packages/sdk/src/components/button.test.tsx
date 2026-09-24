import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Button } from "./button.js";

afterEach(cleanup);

describe("Button", () => {
  it("renders a type=button that fires onClick", () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Save</Button>);
    const button = screen.getByRole("button", { name: "Save" });
    expect(button.getAttribute("type")).toBe("button");
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("submits its form when type is submit", () => {
    const onSubmit = vi.fn((event: SubmitEvent) => event.preventDefault());
    render(
      <form onSubmit={(event) => onSubmit(event.nativeEvent as SubmitEvent)}>
        <Button type="submit">Save</Button>
      </form>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("keeps a loading submit button enabled and focused but blocks a second submit", () => {
    const onSubmit = vi.fn((event: SubmitEvent) => event.preventDefault());
    render(
      <form onSubmit={(event) => onSubmit(event.nativeEvent as SubmitEvent)}>
        <Button type="submit" loading>
          Save
        </Button>
      </form>,
    );
    const button = screen.getByRole("button", { name: "Save" });
    button.focus();
    fireEvent.click(button);
    expect(onSubmit).not.toHaveBeenCalled();
    expect(button.hasAttribute("disabled")).toBe(false);
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(button.getAttribute("aria-disabled")).toBe("true");
    expect(button.hasAttribute("data-disabled")).toBe(false);
    expect(document.activeElement).toBe(button);
  });

  it("marks a disabled button with the native attribute and the dimmed look", () => {
    const onClick = vi.fn();
    render(
      <Button disabled onClick={onClick}>
        Save
      </Button>,
    );
    const button = screen.getByRole("button", { name: "Save" });
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
    expect(button.hasAttribute("disabled")).toBe(true);
    expect(button.getAttribute("data-disabled")).toBe("true");
    expect(button.className).toContain("data-[disabled=true]:opacity-50");
  });

  it("passes native attributes through and forwards its ref", () => {
    const ref = createRef<HTMLButtonElement>();
    render(
      <Button ref={ref} aria-haspopup="menu" data-testid="trigger" name="intent" value="save">
        Save
      </Button>,
    );
    const button = screen.getByTestId("trigger");
    expect(ref.current).toBe(button);
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    expect(button.getAttribute("name")).toBe("intent");
  });

  it("drops a className passed at runtime", () => {
    const props = { className: "mt-4" } as Record<string, unknown>;
    render(<Button {...props}>Save</Button>);
    expect(screen.getByRole("button").className).not.toContain("mt-4");
  });

  it("stretches with fullWidth", () => {
    render(<Button fullWidth>Save</Button>);
    expect(screen.getByRole("button").className).toContain("w-full");
  });

  it("renders the link variant without a fixed height", () => {
    render(<Button variant="link">Sign out</Button>);
    const button = screen.getByRole("button", { name: "Sign out" });
    expect(button.className).toContain("text-primary");
    expect(button.className).not.toContain("h-9");
  });

  describe("with href", () => {
    it("renders an anchor with the variant's styling", () => {
      render(
        <Button href="/auth/login" variant="primary" fullWidth>
          Back to sign in
        </Button>,
      );
      const link = screen.getByRole("link", { name: "Back to sign in" });
      expect(link.getAttribute("href")).toBe("/auth/login");
      expect(link.getAttribute("data-variant")).toBe("primary");
      expect(link.className).toContain("bg-primary");
    });

    it("removes href and sets aria-disabled when disabled", () => {
      const onClick = vi.fn();
      render(
        <Button href="/auth/login" disabled onClick={onClick}>
          Back to sign in
        </Button>,
      );
      const link = screen.getByText("Back to sign in").closest("a");
      expect(link?.hasAttribute("href")).toBe(false);
      expect(link?.getAttribute("aria-disabled")).toBe("true");
      expect(link?.getAttribute("data-disabled")).toBe("true");
      if (link) fireEvent.click(link);
      expect(onClick).not.toHaveBeenCalled();
    });

    it("stays an anchor when a router passes href: undefined", () => {
      render(
        <Button href={undefined} disabled>
          Back to sign in
        </Button>,
      );
      expect(screen.queryByRole("button")).toBeNull();
      expect(screen.getByText("Back to sign in").closest("a")).not.toBeNull();
    });
  });
});
