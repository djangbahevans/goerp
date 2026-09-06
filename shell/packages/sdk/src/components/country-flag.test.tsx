import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { CountryFlag, countryNameOf, flagOf } from "./country-flag.js";

afterEach(cleanup);

describe("flagOf", () => {
  it("builds the regional-indicator flag emoji for an ISO code", () => {
    expect(flagOf("GH")).toBe("🇬🇭");
    expect(flagOf("gh")).toBe("🇬🇭");
  });
});

describe("CountryFlag", () => {
  it("renders the flag with the country name as its accessible label", () => {
    render(<CountryFlag code="GH" />);
    expect(screen.getByRole("img", { name: countryNameOf("GH") }).textContent).toBe(flagOf("GH"));
  });

  it("renders the visible name only when showName is set", () => {
    render(<CountryFlag code="GH" />);
    expect(screen.queryByText(countryNameOf("GH"))).toBeNull();

    cleanup();
    render(<CountryFlag code="GH" showName />);
    expect(screen.getByText(countryNameOf("GH"), { exact: false })).toBeTruthy();
  });
});
