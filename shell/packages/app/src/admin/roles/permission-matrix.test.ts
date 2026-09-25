import { describe, expect, it } from "vitest";
import type { CatalogPermission } from "./admin-roles-api.js";
import { buildMatrix, matrixNames, togglePermission, UNCATALOGUED_SECTION } from "./permission-matrix.js";

const perm = (name: string, category: string, module = name.split(":")[0] ?? ""): CatalogPermission => ({
  name,
  description: `${name} description`,
  category,
  module,
});

const CATALOG: CatalogPermission[] = [
  perm("contacts:contact:financials_read", "Contacts"),
  perm("contacts:contact:merge", "Contacts"),
  perm("contacts:contact:read", "Contacts"),
  perm("contacts:contact:write", "Contacts"),
  perm("contacts:tag:read", "Contacts"),
  perm("contacts:tag:write", "Contacts"),
  perm("sales:order:confirm", "Sales"),
  perm("sales:order:read", "Sales"),
  perm("sales:report:export", "Sales"),
];
const AVAILABLE = matrixNames(buildMatrix(CATALOG, []));

describe("buildMatrix", () => {
  it("groups by category, then by module:resource prefix, in catalog order", () => {
    const sections = buildMatrix(CATALOG, []);
    expect(sections.map((s) => s.title)).toEqual(["Contacts", "Sales"]);
    expect(sections[0]?.groups.map((g) => g.key)).toEqual(["contacts:contact", "contacts:tag"]);
    expect(sections[0]?.groups[0]?.permissions.map((p) => p.name)).toEqual([
      "contacts:contact:financials_read",
      "contacts:contact:merge",
      "contacts:contact:read",
      "contacts:contact:write",
    ]);
  });

  it("falls back to the module name when a permission has no category", () => {
    expect(buildMatrix([perm("hr:employee:read", "", "hr")], [])[0]?.title).toBe("hr");
  });

  it("lists held permissions the catalog doesn't know in a last section", () => {
    const sections = buildMatrix(CATALOG, ["sales:order:read", "legacy:thing:write", "legacy:thing:read"]);
    const last = sections.at(-1);
    expect(last?.title).toBe(UNCATALOGUED_SECTION);
    expect(last?.groups).toEqual([
      {
        key: "legacy:thing",
        permissions: [
          { name: "legacy:thing:read", description: null },
          { name: "legacy:thing:write", description: null },
        ],
      },
    ]);
  });
});

describe("togglePermission", () => {
  const toggle = (selected: string[], name: string, checked: boolean) =>
    [...togglePermission(new Set(selected), name, checked, AVAILABLE)].sort();

  it("selecting a write or action permission selects its group's read", () => {
    expect(toggle([], "contacts:contact:write", true)).toEqual(["contacts:contact:read", "contacts:contact:write"]);
    expect(toggle([], "contacts:contact:merge", true)).toEqual(["contacts:contact:merge", "contacts:contact:read"]);
    expect(toggle([], "sales:order:confirm", true)).toEqual(["sales:order:confirm", "sales:order:read"]);
  });

  it("only selects the read of the permission's own group", () => {
    expect(toggle([], "contacts:tag:write", true)).toEqual(["contacts:tag:read", "contacts:tag:write"]);
  });

  it("selects nothing extra when the group has no read permission", () => {
    expect(toggle([], "sales:report:export", true)).toEqual(["sales:report:export"]);
  });

  it("selecting read selects only read", () => {
    expect(toggle([], "contacts:contact:read", true)).toEqual(["contacts:contact:read"]);
  });

  it("deselecting read deselects every other permission in its group, and only its group", () => {
    const selected = [
      "contacts:contact:financials_read",
      "contacts:contact:merge",
      "contacts:contact:read",
      "contacts:contact:write",
      "contacts:tag:read",
      "contacts:tag:write",
    ];
    expect(toggle(selected, "contacts:contact:read", false)).toEqual(["contacts:tag:read", "contacts:tag:write"]);
  });

  it("deselecting a write permission leaves read selected", () => {
    expect(toggle(["contacts:contact:read", "contacts:contact:write"], "contacts:contact:write", false)).toEqual([
      "contacts:contact:read",
    ]);
  });

  it("does not change the set it was given", () => {
    const selected = new Set(["contacts:contact:read"]);
    togglePermission(selected, "contacts:contact:write", true, AVAILABLE);
    expect([...selected]).toEqual(["contacts:contact:read"]);
  });
});
