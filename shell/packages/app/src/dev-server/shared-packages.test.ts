import { describe, expect, it } from "vitest";
import { SHARED_PACKAGES } from "./shared-packages.js";

describe("shared packages", () => {
  it("shares runtime SDK entries while excluding test utilities", () => {
    expect(SHARED_PACKAGES).toContain("@goerp/sdk");
    expect(SHARED_PACKAGES).toContain("@goerp/sdk/realtime");
    expect(SHARED_PACKAGES).not.toContain("@goerp/sdk/testing");
    expect(SHARED_PACKAGES).not.toContain("@testing-library/react");
  });
});
