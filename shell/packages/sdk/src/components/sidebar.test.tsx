import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Sidebar } from "./sidebar.js";

afterEach(cleanup);

describe("Sidebar", () => {
  it("renders its children", () => {
    render(
      <Sidebar>
        <p>Related records</p>
      </Sidebar>,
    );
    expect(screen.getByText("Related records")).toBeTruthy();
  });

  it("defaults to a 280px width", () => {
    render(
      <Sidebar>
        <p>Related records</p>
      </Sidebar>,
    );
    const aside = screen.getByText("Related records").closest("aside") as HTMLElement;
    expect(aside.style.width).toBe("280px");
  });

  it("applies a custom width when provided", () => {
    render(
      <Sidebar width={320}>
        <p>Related records</p>
      </Sidebar>,
    );
    const aside = screen.getByText("Related records").closest("aside") as HTMLElement;
    expect(aside.style.width).toBe("320px");
  });
});
