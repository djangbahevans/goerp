import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { BadgeColor } from "./badge.js";
import { StatusDot } from "./status-dot.js";

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

  it("falls back to gray for an unrecognized color, e.g. a mistyped manifest value", () => {
    render(<StatusDot color={"chartreuse" as BadgeColor} />);
    expect(screen.getByRole("img").className).toContain("bg-gray-400");
  });
});
