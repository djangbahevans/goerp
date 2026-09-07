import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { CountryFlag, countryNameOf } from "./country-flag.js";

afterEach(cleanup);

describe("CountryFlag", () => {
  it("renders the flag-icons class for the code, with the country name as its accessible label", () => {
    render(<CountryFlag code="GH" />);
    const flag = screen.getByRole("img", { name: countryNameOf("GH") });
    expect(flag.className).toContain("fi-gh");
  });

  it("lowercases the code for the flag-icons class", () => {
    render(<CountryFlag code="gh" />);
    expect(screen.getByRole("img").className).toContain("fi-gh");
  });

  it("renders the visible name only when showName is set", () => {
    render(<CountryFlag code="GH" />);
    expect(screen.queryByText(countryNameOf("GH"))).toBeNull();

    cleanup();
    render(<CountryFlag code="GH" showName />);
    expect(screen.getByText(countryNameOf("GH"), { exact: false })).toBeTruthy();
  });

  it("falls back to the raw code as plain text for an invalid code, never a broken glyph", () => {
    render(<CountryFlag code="123" />);
    expect(screen.getByRole("img").textContent).toBe("123");
  });

  it("falls back to plain text for a well-formed code flag-icons has no icon for, e.g. the 'UK' alias for 'GB'", () => {
    render(<CountryFlag code="UK" />);
    const flag = screen.getByRole("img");
    expect(flag.textContent).toBe("UK");
    expect(flag.className).toBe("");
  });
});
