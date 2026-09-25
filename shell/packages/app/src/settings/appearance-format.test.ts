import { describe, expect, it } from "vitest";
import { formatDateAs, nativeLocaleName, timezonePreview } from "./appearance-format.js";

describe("nativeLocaleName", () => {
  it("names a locale in its own language, capitalized", () => {
    expect(nativeLocaleName("en")).toBe("English");
    expect(nativeLocaleName("fr")).toBe("Français");
  });

  it("falls back to the code for a tag the runtime can't parse", () => {
    expect(nativeLocaleName("not a locale")).toBe("not a locale");
  });
});

describe("formatDateAs", () => {
  const date = new Date(2026, 4, 16);

  it("formats each date order", () => {
    expect(formatDateAs(date, "day_first", "en")).toBe("16 May 2026");
    expect(formatDateAs(date, "month_first", "en")).toBe("May 16, 2026");
    expect(formatDateAs(date, "iso", "en")).toBe("2026-05-16");
  });

  it("zero-pads the ISO month and day", () => {
    expect(formatDateAs(new Date(2026, 0, 5), "iso", "en")).toBe("2026-01-05");
  });
});

describe("timezonePreview", () => {
  it("shows the zone and its current time", () => {
    expect(timezonePreview("UTC", new Date(Date.UTC(2026, 4, 16, 10, 22)), "en-GB")).toBe("UTC — 10:22");
  });

  it("returns null for an unknown zone", () => {
    expect(timezonePreview("Mars/Olympus", new Date(), "en")).toBeNull();
  });
});
