import { TagPill } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import type { Row } from "./list/list-view-types.js";
import { recordValueText } from "./record-values.js";

// Tag objects render in their own color; bare IDs (a record without its tag objects) render as
// plain pills showing the ID.
export function TagPills({ tags }: { tags: unknown[] }): ReactNode {
  return (
    <span className="inline-flex flex-wrap gap-1">
      {tags.map((tag) => {
        const row = typeof tag === "object" && tag !== null ? (tag as Row) : null;
        const color = typeof row?.color === "string" && row.color !== "" ? row.color : undefined;
        return <TagPill key={String(row ? row.id : tag)} name={recordValueText(tag)} color={color} />;
      })}
    </span>
  );
}
