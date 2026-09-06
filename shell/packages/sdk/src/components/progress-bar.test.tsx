import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ProgressBar } from "./progress-bar.js";

afterEach(cleanup);

describe("ProgressBar", () => {
  it("exposes value via ARIA progressbar attributes", () => {
    render(<ProgressBar value={75} label="75% complete" />);
    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("75");
    expect(bar.getAttribute("aria-valuemin")).toBe("0");
    expect(bar.getAttribute("aria-valuemax")).toBe("100");
    expect(bar.getAttribute("aria-label")).toBe("75% complete");
  });

  it("clamps an out-of-range value into 0-100", () => {
    render(<ProgressBar value={150} />);
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe("100");

    cleanup();
    render(<ProgressBar value={-10} />);
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe("0");
  });

  it("falls back to 0 for a NaN value (e.g. done / total before total is known)", () => {
    render(<ProgressBar value={0 / 0} />);
    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("0");
    expect(bar.querySelector("div")?.style.width).toBe("0%");
  });

  it("renders the visible label only when showLabel is true", () => {
    render(<ProgressBar value={75} label="75% complete" />);
    expect(screen.queryByText("75% complete")).toBeNull();

    cleanup();
    render(<ProgressBar value={75} label="75% complete" showLabel />);
    expect(screen.getByText("75% complete")).toBeTruthy();
  });
});
