import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TagPill } from "./tag-pill.js";

afterEach(cleanup);

describe("TagPill", () => {
  it("fills with the tag's color and picks the higher-contrast text", () => {
    render(
      <>
        <TagPill name="Dark" color="#1E3A8A" />
        <TagPill name="Light" color="#FDE68A" />
      </>,
    );
    const dark = screen.getByText("Dark");
    const light = screen.getByText("Light");
    expect(dark.style.backgroundColor).toBe("rgb(30, 58, 138)");
    expect(dark.className).toContain("text-white");
    expect(light.className).toContain("text-black");
  });

  it("renders a neutral pill for a tag with no color", () => {
    render(<TagPill name="Plain" />);
    const pill = screen.getByText("Plain");
    expect(pill.style.backgroundColor).toBe("");
    expect(pill.className).toContain("bg-bg-subtle");
  });
});
