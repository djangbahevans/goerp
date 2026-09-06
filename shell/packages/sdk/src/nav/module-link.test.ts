import { describe, expect, it } from "vitest";
import { moduleLink } from "./module-link.js";

describe("moduleLink", () => {
  it("prefixes an expanded API path with /_m for browser navigation", () => {
    expect(moduleLink("/contacts")).toBe("/_m/contacts");
    expect(moduleLink("/contacts/01j...")).toBe("/_m/contacts/01j...");
  });
});
