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

  it("defaults to a 280px width from 768px up", () => {
    render(
      <Sidebar>
        <p>Related records</p>
      </Sidebar>,
    );
    const aside = screen.getByText("Related records").closest("aside") as HTMLElement;
    expect(aside.style.getPropertyValue("--sidebar-width")).toBe("280px");
  });

  it("applies a custom width when provided", () => {
    render(
      <Sidebar width={320}>
        <p>Related records</p>
      </Sidebar>,
    );
    const aside = screen.getByText("Related records").closest("aside") as HTMLElement;
    expect(aside.style.getPropertyValue("--sidebar-width")).toBe("320px");
  });

  it("stacks under the content below 768px and sits beside it from 768px up", () => {
    render(
      <Sidebar>
        <p>Related records</p>
      </Sidebar>,
    );
    const aside = screen.getByText("Related records").closest("aside") as HTMLElement;
    for (const name of ["border-t", "md:w-(--sidebar-width)", "md:border-t-0", "md:border-l"]) {
      expect(aside.classList.contains(name)).toBe(true);
    }
    expect(aside.style.width).toBe("");
  });
});
