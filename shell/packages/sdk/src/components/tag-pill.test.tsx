import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TagPill } from "./tag-pill.js";

afterEach(cleanup);

describe("TagPill", () => {
  it("fills with a hex color and picks the higher-contrast text", () => {
    render(
      <>
        <TagPill name="Dark" color="#1E3A8A" />
        <TagPill name="Light" color="#FDE68A" />
        <TagPill name="Short" color="#000" />
      </>,
    );
    expect(screen.getByText("Dark").style.backgroundColor).toBe("rgb(30, 58, 138)");
    expect(screen.getByText("Dark").className).toContain("text-white");
    expect(screen.getByText("Light").className).toContain("text-black");
    expect(screen.getByText("Short").className).toContain("text-white");
  });

  it.each([undefined, "green", "rgb(0, 128, 0)", "#12345"])("renders the neutral pill for color %s", (color) => {
    render(<TagPill name="Plain" color={color} />);
    const pill = screen.getByText("Plain");
    expect(pill.style.backgroundColor).toBe("");
    expect(pill.className).toContain("bg-bg-subtle");
  });

  it("matches Badge's metrics by default and grows for a control at md", () => {
    render(
      <>
        <TagPill name="Display" />
        <TagPill name="Editable" size="md" />
      </>,
    );
    expect(screen.getByText("Display").className).toContain("text-xs");
    expect(screen.getByText("Display").className).toContain("py-0.5");
    expect(screen.getByText("Editable").className).toContain("text-sm");
    expect(screen.getByText("Editable").className).toContain("max-md:min-h-11");
  });
});
