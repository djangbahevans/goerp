import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { LoadingOverlay } from "./loading-overlay.js";

afterEach(cleanup);

describe("LoadingOverlay", () => {
  it("renders a status region announcing 'Loading' by default", () => {
    render(<LoadingOverlay />);
    const status = screen.getByRole("status");
    expect(status.textContent).toBe("Loading");
  });

  it("renders a custom label", () => {
    render(<LoadingOverlay label="Saving contact…" />);
    expect(screen.getByRole("status").textContent).toBe("Saving contact…");
  });

  it("positions itself absolutely to overlay its parent", () => {
    render(<LoadingOverlay />);
    expect(screen.getByRole("status").style.position).toBe("absolute");
  });

  it("renders a visible backdrop, not just floating text, and marks itself busy", () => {
    render(<LoadingOverlay />);
    const overlay = screen.getByRole("status");
    expect(overlay.style.background).not.toBe("");
    expect(overlay.getAttribute("aria-busy")).toBe("true");
  });
});
