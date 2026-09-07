import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PageLayout } from "./page-layout.js";

afterEach(cleanup);

describe("PageLayout", () => {
  it("renders its children", () => {
    render(
      <PageLayout>
        <p>Page content</p>
      </PageLayout>,
    );
    expect(screen.getByText("Page content")).toBeTruthy();
  });

  it("applies consistent page padding using the shell's design tokens", () => {
    const { container } = render(
      <PageLayout>
        <p>Page content</p>
      </PageLayout>,
    );
    const wrapper = container.firstElementChild;
    expect(wrapper?.className).toContain("p-6");
    expect(wrapper?.className).toContain("bg-bg");
    expect(wrapper?.className).toContain("text-text");
  });
});
