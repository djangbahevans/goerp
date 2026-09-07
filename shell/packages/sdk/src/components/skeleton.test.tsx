import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Skeleton } from "./skeleton.js";

afterEach(cleanup);

describe("Skeleton", () => {
  it("renders `lines` placeholder bars by default", () => {
    const { container } = render(<Skeleton lines={3} />);
    expect(container.querySelectorAll("[data-skeleton-line]")).toHaveLength(3);
  });

  it("defaults to 3 lines when lines is omitted", () => {
    const { container } = render(<Skeleton />);
    expect(container.querySelectorAll("[data-skeleton-line]")).toHaveLength(3);
  });

  it("renders a single card placeholder for type='card'", () => {
    const { container } = render(<Skeleton type="card" />);
    expect(container.querySelector('[data-skeleton="card"]')).toBeTruthy();
    expect(container.querySelectorAll("[data-skeleton-line]")).toHaveLength(0);
  });

  it("renders a rows x columns grid for type='table'", () => {
    const { container } = render(<Skeleton type="table" rows={5} columns={4} />);
    expect(container.querySelectorAll("[data-skeleton-row]")).toHaveLength(5);
    expect(container.querySelectorAll("[data-skeleton-cell]")).toHaveLength(20);
  });

  it("marks the outer container busy, and only the decorative shapes hidden — never the same element", () => {
    const { container } = render(<Skeleton />);
    const outer = container.querySelector('[data-skeleton="lines"]');
    expect(outer?.getAttribute("aria-busy")).toBe("true");
    expect(outer?.getAttribute("aria-hidden")).toBeNull();

    const line = container.querySelector("[data-skeleton-line]");
    expect(line?.getAttribute("aria-hidden")).toBe("true");
    expect(line?.getAttribute("aria-busy")).toBeNull();
  });

  it("marks card and table containers busy with hidden inner shapes too", () => {
    const { container: cardContainer } = render(<Skeleton type="card" />);
    expect(cardContainer.querySelector('[data-skeleton="card"]')?.getAttribute("aria-busy")).toBe("true");
    expect(cardContainer.querySelector('[data-skeleton="card"] > [aria-hidden="true"]')).not.toBeNull();

    const { container: tableContainer } = render(<Skeleton type="table" />);
    expect(tableContainer.querySelector('[data-skeleton="table"]')?.getAttribute("aria-busy")).toBe("true");
    expect(tableContainer.querySelector("[data-skeleton-cell]")?.getAttribute("aria-hidden")).toBe("true");
  });
});
