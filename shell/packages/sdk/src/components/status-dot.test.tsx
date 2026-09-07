import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { StatusDot, type StatusDotColor } from "./status-dot.js";

afterEach(cleanup);

describe("StatusDot", () => {
  it("renders the visible label", () => {
    render(<StatusDot color="green" label="Active" />);
    expect(screen.getByText("Active")).toBeTruthy();
  });

  it("applies animate-pulse only when pulse is set", () => {
    render(<StatusDot color="red" label="Overdue" pulse />);
    expect(screen.getByRole("img").className).toContain("animate-pulse");

    cleanup();
    render(<StatusDot color="red" label="Overdue" />);
    expect(screen.getByRole("img").className).not.toContain("animate-pulse");
  });

  it("falls back to the color as the accessible name when label is omitted", () => {
    render(<StatusDot color="green" />);
    expect(screen.getByRole("img").getAttribute("aria-label")).toBe("green");
  });

  it("renders each of the four functional status colors", () => {
    for (const color of ["green", "red", "orange", "blue"] as const) {
      cleanup();
      render(<StatusDot color={color} />);
      expect(screen.getByRole("img").getAttribute("aria-label")).toBe(color);
    }
  });

  it("falls back to gray for an unrecognized color, e.g. a mistyped manifest value", () => {
    render(<StatusDot color={"chartreuse" as StatusDotColor} />);
    expect(screen.getByRole("img").className).toContain("bg-text-disabled");
  });

  it("disables the pulse animation under prefers-reduced-motion", () => {
    render(<StatusDot color="red" label="Overdue" pulse />);
    expect(screen.getByRole("img").className).toContain("motion-reduce:animate-none");
  });
});
