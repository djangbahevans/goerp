import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { parseManifestValue } from "@goerp/sdk/schema";
import { describe, expect, it } from "vitest";
import { AnyViewDeclarationSchema, KNOWN_VIEW_TYPES } from "./view-dispatch.js";

// The engine's own tests decode these same documents into manifest.View and
// serve them through /_meta/schema, so a view shape that only one side
// accepts fails in that side's suite.
const fixtureDir = fileURLToPath(new URL("../../../../../testdata/manifest-views/", import.meta.url));
const fixtures = readdirSync(fixtureDir)
  .filter((file) => file.endsWith(".json"))
  .map((file) => ({ file, view: JSON.parse(readFileSync(`${fixtureDir}${file}`, "utf8")) as { type: string } }));

describe("manifest view fixtures shared with the engine", () => {
  it("covers every view type the engine documents", () => {
    const types = new Set(fixtures.map(({ view }) => view.type));
    expect([...types].sort()).toEqual(["calendar", "custom", "form", "kanban", "list", "pivot", "timeline"]);
  });

  it.each(fixtures.filter(({ view }) => KNOWN_VIEW_TYPES.has(view.type)))(
    "$file parses against its per-type declaration schema",
    ({ view }) => {
      const result = parseManifestValue(AnyViewDeclarationSchema, view);
      expect(result.success, result.success ? "" : result.message).toBe(true);
    },
  );
});
