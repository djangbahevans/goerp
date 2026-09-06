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

  it("is hidden from assistive technology", () => {
    const { container } = render(<Skeleton />);
    expect(container.querySelector('[data-skeleton="lines"]')?.getAttribute("aria-hidden")).toBe("true");
  });
});
