import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TwoColumnLayout } from "./two-column-layout.js";

afterEach(cleanup);

describe("TwoColumnLayout", () => {
  it("renders both children and sidebar", () => {
    render(<TwoColumnLayout sidebar={<p>Sidebar content</p>}>Main content</TwoColumnLayout>);
    expect(screen.getByText("Main content")).toBeTruthy();
    expect(screen.getByText("Sidebar content")).toBeTruthy();
  });

  it("renders a 2/3 + 1/3 split", () => {
    const { container } = render(<TwoColumnLayout sidebar={<p>Sidebar content</p>}>Main content</TwoColumnLayout>);
    const [main, sidebar] = container.firstElementChild?.children ?? [];
    expect(main?.className).toContain("w-2/3");
    expect(sidebar?.className).toContain("w-1/3");
  });
});
