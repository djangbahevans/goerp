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
  it("right-aligns its controls only in a wide toolbar", () => {
    render(<ViewToolbar filters={null} controls={<button type="button">Columns</button>} />);

    const classes = screen.getByRole("button", { name: "Columns" }).parentElement?.classList;
    expect(classes?.contains("@2xl:ms-auto")).toBe(true);
    expect(classes?.contains("@2xl:justify-end")).toBe(true);
    expect(classes?.contains("ms-auto")).toBe(false);
  });

  it("renders no controls cluster when given no controls", () => {
    const { container } = render(<ViewToolbar filters={null} />);

    expect(container.firstElementChild?.childElementCount).toBe(0);
  });
});
