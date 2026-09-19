/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";

const modules = import.meta.glob<string>(["./*.ts", "!./*.test.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
});

const FORBIDDEN = [
  /\beval\s*\(/,
  /\bnew\s+Function\b/,
  /(?<![\w.])Function\s*\(/,
  /\bimport\s*\(/,
  /\bsetTimeout\s*\(\s*["'`]/,
];

describe("domain expression module", () => {
  it("has sources to check", () => {
    expect(Object.keys(modules)).toEqual(expect.arrayContaining(["./parser.ts", "./interpreter.ts", "./lexer.ts"]));
  });

  it.each(Object.entries(modules))("%s contains no dynamic code execution", (_path, text) => {
    for (const pattern of FORBIDDEN) expect(text).not.toMatch(pattern);
  });
});
