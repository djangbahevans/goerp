import { describe, expect, it } from "vitest";
import { formatPhone } from "./phone-format.js";

describe("formatPhone", () => {
  it.each([
    ["+233244123456", "+233 24 412 3456", "tel:+233244123456"],
    ["+1 (202) 555-0123", "+1 202 555 0123", "tel:+12025550123"],
    ["+233 050 123-4567", "+233 50 123 4567", "tel:+233501234567"],
    ["+233 24 412 3456 ext. 12", "+233 24 412 3456 ext. 12", "tel:+233244123456;ext=12"],
    ["+12025550123x45", "+1 202 555 0123 ext. 45", "tel:+12025550123;ext=45"],
    ["+800 1234 5678", "+800 1234 5678", "tel:+80012345678"],
  ])("formats the international value %j", (value, text, href) => {
    expect(formatPhone(value)).toEqual({ text, href });
  });

  it.each([
    ["national", "0244123456"],
    ["00-prefixed", "00233244123456"],
    ["unknown calling code", "+999 123456"],
    ["bare calling code", "+233"],
    ["impossible length", "+1 555"],
    ["trailing note", "+233 24-412-3456 (office)"],
    ["empty", ""],
  ])("keeps a %s value verbatim", (_, value) => {
    expect(formatPhone(value)).toEqual({ text: value, href: `tel:${value}` });
  });
});
