import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Spinner } from "./spinner.js";

afterEach(cleanup);

describe("Spinner", () => {
  it("renders an svg sized to the given size", () => {
    const { container } = render(<Spinner size={24} />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("width")).toBe("24");
    expect(svg?.getAttribute("height")).toBe("24");
  });

  it("is decorative, hidden from assistive technology", () => {
    const { container } = render(<Spinner size={16} />);
    expect(container.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("disables its spin animation under prefers-reduced-motion", () => {
    const { container } = render(<Spinner size={16} />);
    expect(container.querySelector("svg")?.getAttribute("class")).toContain("motion-reduce:animate-none");
  });
});
