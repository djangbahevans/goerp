import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ViewPage, ViewToolbar } from "./view-chrome.js";

afterEach(cleanup);

describe("ViewPage", () => {
  it("renders a PageHeader titled with the view label, with its actions, when full-page", () => {
    render(
      <ViewPage embedded={false} title="Orders" actions={<button type="button">New Order</button>}>
        <p>body</p>
      </ViewPage>,
    );

    const heading = screen.getByRole("heading", { level: 1, name: "Orders" });
    expect(heading.closest("header")?.textContent).toContain("New Order");
    expect(screen.getByText("body")).toBeTruthy();
  });

  it("renders only its children when embedded", () => {
    render(
      <ViewPage embedded title="Orders" actions={<button type="button">New Order</button>}>
        <p>body</p>
      </ViewPage>,
    );

    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.queryByText("New Order")).toBeNull();
    expect(screen.getByText("body")).toBeTruthy();
  });
});

describe("ViewToolbar", () => {
  it("wraps its controls in a right-aligned cluster", () => {
    render(<ViewToolbar filters={null} controls={<button type="button">Columns</button>} />);

    expect(screen.getByRole("button", { name: "Columns" }).parentElement?.className).toContain("ms-auto");
  });

  it("renders no controls cluster when given no controls", () => {
    const { container } = render(<ViewToolbar filters={null} />);

    expect(container.firstElementChild?.childElementCount).toBe(0);
  });
});
