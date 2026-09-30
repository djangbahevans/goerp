import { describe, expect, it } from "vitest";
import { slugifyActivityTypeKey } from "./slugify-activity-type-key.js";

describe("slugifyActivityTypeKey", () => {
  it("strips accents and lowercases", () => {
    expect(slugifyActivityTypeKey("Réunion")).toBe("reunion");
  });

  it("prefixes a label that doesn't start with a letter", () => {
    expect(slugifyActivityTypeKey("1st call")).toBe("type_1st_call");
  });

  it("falls back to type for a label with nothing left after stripping", () => {
    expect(slugifyActivityTypeKey("見学")).toBe("type");
  });

  it("collapses runs of other characters into one underscore and trims", () => {
    expect(slugifyActivityTypeKey("  Site   Visit!! ")).toBe("site_visit");
  });

  it("clamps to 40 characters", () => {
    const long = "a".repeat(60);
    expect(slugifyActivityTypeKey(long)).toHaveLength(40);
  });
});
