import type { PivotHeaderNode } from "./pivot-view-types.js";

// One <th> worth of layout for a nested pivot header (row headers run
// top-to-bottom in depth, column headers left-to-right).
export interface PivotHeaderCell {
  node: PivotHeaderNode;
  depth: number;
  // Leaf rows/columns spanned along this cell's own axis.
  span: number;
  // Header levels spanned along the *opposite* axis: 1 for an expanded
  // group, maxDepth - depth for a leaf or collapsed group.
  crossSpan: number;
  hasChildren: boolean;
  isCollapsed: boolean;
  // Position, among this axis's visible leaves, where a tbody row (or
  // column) should render this cell.
  startIndex: number;
  // Every visible leaf key under this cell, including itself when it is one.
  leafKeys: string[];
}

function isCollapsedOrLeaf(node: PivotHeaderNode, collapsed: ReadonlySet<string>): boolean {
  return !node.children || node.children.length === 0 || collapsed.has(node.key);
}

// Depth of the full tree, ignoring collapse — this axis's opposite-side
// header row/column count is constant regardless of what's collapsed.
export function treeDepth(nodes: PivotHeaderNode[]): number {
  let max = 1;
  for (const node of nodes) {
    if (node.children && node.children.length > 0) {
      max = Math.max(max, 1 + treeDepth(node.children));
    }
  }
  return max;
}

// One array of header cells per depth level (index 0 = outermost).
export function layoutHeaderLevels(nodes: PivotHeaderNode[], collapsed: ReadonlySet<string>): PivotHeaderCell[][] {
  const maxDepth = treeDepth(nodes);
  const levels: PivotHeaderCell[][] = Array.from({ length: maxDepth }, () => []);
  let leafCounter = 0;

  function walk(node: PivotHeaderNode, depth: number): string[] {
    const startIndex = leafCounter;
    const collapsedHere = isCollapsedOrLeaf(node, collapsed);
    let leafKeys: string[];
    if (collapsedHere) {
      leafKeys = [node.key];
      leafCounter += 1;
    } else {
      leafKeys = [];
      for (const child of node.children ?? []) leafKeys.push(...walk(child, depth + 1));
    }
    levels[depth]?.push({
      node,
      depth,
      span: leafKeys.length,
      crossSpan: collapsedHere ? maxDepth - depth : 1,
      hasChildren: Boolean(node.children && node.children.length > 0),
      isCollapsed: collapsed.has(node.key),
      startIndex,
      leafKeys,
    });
    return leafKeys;
  }

  for (const node of nodes) walk(node, 0);
  return levels;
}

// Currently-visible leaves in document order — a collapsed group counts as
// its own leaf.
export function visibleLeaves(nodes: PivotHeaderNode[], collapsed: ReadonlySet<string>): PivotHeaderNode[] {
  return nodes.flatMap((node) =>
    isCollapsedOrLeaf(node, collapsed) ? [node] : visibleLeaves(node.children ?? [], collapsed),
  );
}

// Full ancestor-to-leaf accessibleLabel chain per visible leaf — the
// row/column context a body cell announces alongside its value.
export function leafAccessiblePaths(nodes: PivotHeaderNode[], collapsed: ReadonlySet<string>): Map<string, string[]> {
  const result = new Map<string, string[]>();

  function walk(node: PivotHeaderNode, path: string[]): void {
    const nextPath = [...path, node.accessibleLabel];
    if (isCollapsedOrLeaf(node, collapsed)) {
      result.set(node.key, nextPath);
      return;
    }
    for (const child of node.children ?? []) walk(child, nextPath);
  }

  for (const node of nodes) walk(node, []);
  return result;
}
