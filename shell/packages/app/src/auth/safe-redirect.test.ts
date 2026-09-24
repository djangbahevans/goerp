import { describe, expect, it } from "vitest";
import { safeRedirect } from "./safe-redirect.js";

describe("safeRedirect", () => {
  it("keeps a same-origin path, including its query string", () => {
    expect(safeRedirect("/_m/sales/orders?status=open")).toBe("/_m/sales/orders?status=open");
  });

  it.each([
    ["missing", undefined],
    ["non-string", 42],
    ["absolute URL", "https://evil.example/phish"],
    ["protocol-relative", "//evil.example"],
    ["backslash protocol-relative", "/\\evil.example"],
    ["relative path", "settings/profile"],
    ["auth route", "/auth/login"],
    ["bare auth path with a query", "/auth?next=1"],
  ])("falls back to / for a %s target", (_label, raw) => {
    expect(safeRedirect(raw)).toBe("/");
  });
});
