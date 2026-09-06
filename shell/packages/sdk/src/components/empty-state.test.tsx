import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EmptyState } from "./empty-state.js";

afterEach(cleanup);

describe("EmptyState", () => {
  it("renders title and description", () => {
    render(<EmptyState title="No orders yet" description="Create your first order to get started" />);
    expect(screen.getByText("No orders yet")).toBeTruthy();
    expect(screen.getByText("Create your first order to get started")).toBeTruthy();
  });

  it("renders no description element when omitted", () => {
    const { container } = render(<EmptyState title="No orders yet" />);
    expect(container.querySelector("p")).toBeNull();
  });

  it("renders the given action content", () => {
    render(<EmptyState title="No orders yet" action={<button type="button">New Order</button>} />);
    expect(screen.getByRole("button", { name: "New Order" })).toBeTruthy();
  });
});
