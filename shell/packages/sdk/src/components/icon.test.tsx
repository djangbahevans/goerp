import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Icon, isKnownIconName } from "./icon.js";

afterEach(cleanup);

describe("isKnownIconName", () => {
  it("recognizes a real Lucide icon name", () => {
    expect(isKnownIconName("shopping-cart")).toBe(true);
  });

  it("rejects an unrecognized name", () => {
    expect(isKnownIconName("not-a-real-icon")).toBe(false);
  });
});

describe("Icon", () => {
  it("renders the resolved icon's svg once its chunk loads", async () => {
    const { container } = render(<Icon name="users" aria-hidden />);
    await waitFor(() => expect(container.querySelector("svg")).toBeTruthy());
  });

  it("renders nothing for an unrecognized icon name", () => {
    const { container } = render(<Icon name="not-a-real-icon" />);
    expect(container.querySelector("svg")).toBeNull();
    expect(container.firstChild).toBeNull();
  });

  it("passes through size/className to the rendered svg", async () => {
    const { container } = render(<Icon name="users" size={20} className="text-primary" aria-hidden />);
    await waitFor(() => expect(container.querySelector("svg")).toBeTruthy());
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("width")).toBe("20");
    expect(svg?.getAttribute("class")).toContain("text-primary");
  });
});
