import { describe, expect, it } from "vitest";
import { layoutHeaderLevels, leafAccessiblePaths, treeDepth, visibleLeaves } from "./pivot-grid-layout.js";
import type { PivotHeaderNode } from "./pivot-view-types.js";

// view-system.md §8's sales_pivot example, two rows deep: customer_name >
// (an implied second level, exercised here) with two customers under one
// region and one under another.
function twoLevelTree(): PivotHeaderNode[] {
  return [
    {
      key: "emea",
      label: "EMEA",
      accessibleLabel: "EMEA",
      children: [
        { key: "emea.acme", label: "Acme Corp", accessibleLabel: "Acme Corp" },
        { key: "emea.globex", label: "Globex Inc", accessibleLabel: "Globex Inc" },
      ],
    },
    {
      key: "apac",
      label: "APAC",
      accessibleLabel: "APAC",
      children: [{ key: "apac.initech", label: "Initech", accessibleLabel: "Initech" }],
    },
  ];
}

describe("treeDepth", () => {
  it("returns 1 for a flat single-level tree", () => {
    expect(treeDepth([{ key: "a", label: "A", accessibleLabel: "A" }])).toBe(1);
  });

  it("returns the deepest branch's depth for a nested tree", () => {
    expect(treeDepth(twoLevelTree())).toBe(2);
  });
});

describe("visibleLeaves", () => {
  it("returns every leaf when nothing is collapsed", () => {
    const leaves = visibleLeaves(twoLevelTree(), new Set());
    expect(leaves.map((leaf) => leaf.key)).toEqual(["emea.acme", "emea.globex", "apac.initech"]);
  });

  it("treats a collapsed group as its own single leaf", () => {
    const leaves = visibleLeaves(twoLevelTree(), new Set(["emea"]));
    expect(leaves.map((leaf) => leaf.key)).toEqual(["emea", "apac.initech"]);
  });
});

describe("layoutHeaderLevels", () => {
  it("gives each level's cells a startIndex matching document order, expanded", () => {
    const levels = layoutHeaderLevels(twoLevelTree(), new Set());
    expect(levels).toHaveLength(2);
    expect(levels[0]?.map((cell) => ({ key: cell.node.key, span: cell.span, startIndex: cell.startIndex }))).toEqual([
      { key: "emea", span: 2, startIndex: 0 },
      { key: "apac", span: 1, startIndex: 2 },
    ]);
    expect(levels[1]?.map((cell) => ({ key: cell.node.key, span: cell.span, startIndex: cell.startIndex }))).toEqual([
      { key: "emea.acme", span: 1, startIndex: 0 },
      { key: "emea.globex", span: 1, startIndex: 1 },
      { key: "apac.initech", span: 1, startIndex: 2 },
    ]);
  });

  it("gives an expanded group crossSpan 1 and a leaf crossSpan spanning the remaining depth", () => {
    const levels = layoutHeaderLevels(twoLevelTree(), new Set());
    const emea = levels[0]?.find((cell) => cell.node.key === "emea");
    const acme = levels[1]?.find((cell) => cell.node.key === "emea.acme");
    expect(emea?.crossSpan).toBe(1);
    expect(acme?.crossSpan).toBe(1);
  });

  it("collapses a group to span 1 leaf and fill the remaining depth via crossSpan", () => {
    const levels = layoutHeaderLevels(twoLevelTree(), new Set(["emea"]));
    const emea = levels[0]?.find((cell) => cell.node.key === "emea");
    expect(emea?.span).toBe(1);
    // maxDepth (2) - depth (0) = 2 header columns/rows worth of width.
    expect(emea?.crossSpan).toBe(2);
    expect(emea?.isCollapsed).toBe(true);
    // The collapsed group's children no longer appear at the deeper level.
    expect(levels[1]?.some((cell) => cell.node.key.startsWith("emea."))).toBe(false);
    // apac, unaffected, keeps its own startIndex shifted by the collapse.
    const apac = levels[0]?.find((cell) => cell.node.key === "apac");
    expect(apac?.startIndex).toBe(1);
  });

  it("collects every visible leaf key under a cell into leafKeys", () => {
    const levels = layoutHeaderLevels(twoLevelTree(), new Set());
    const emea = levels[0]?.find((cell) => cell.node.key === "emea");
    expect(emea?.leafKeys).toEqual(["emea.acme", "emea.globex"]);
  });
});

describe("leafAccessiblePaths", () => {
  it("maps each visible leaf to its full ancestor label chain", () => {
    const paths = leafAccessiblePaths(twoLevelTree(), new Set());
    expect(paths.get("emea.acme")).toEqual(["EMEA", "Acme Corp"]);
    expect(paths.get("apac.initech")).toEqual(["APAC", "Initech"]);
  });

  it("stops the chain at a collapsed group, since it is the leaf now", () => {
    const paths = leafAccessiblePaths(twoLevelTree(), new Set(["emea"]));
    expect(paths.get("emea")).toEqual(["EMEA"]);
    expect(paths.has("emea.acme")).toBe(false);
  });
});
