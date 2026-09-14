import { describe, expect, it } from "vitest";
import type { Row } from "../list/list-view-types.js";
import { bucketRowsByGroup, deriveGroupIds, splitCardFields } from "./kanban-grouping.js";

describe("splitCardFields", () => {
  it("pulls avatar_file_id out regardless of position, and takes the first remaining field as the title", () => {
    expect(splitCardFields(["avatar_file_id", "display_name", "email", "phone"])).toEqual({
      avatarField: "avatar_file_id",
      titleField: "display_name",
      secondaryFields: ["email", "phone"],
    });
  });

  it("has no avatar field when card_fields doesn't declare one", () => {
    expect(splitCardFields(["display_name", "email"])).toEqual({
      avatarField: undefined,
      titleField: "display_name",
      secondaryFields: ["email"],
    });
  });

  it("has no secondary fields when card_fields has just one entry", () => {
    expect(splitCardFields(["display_name"])).toEqual({
      avatarField: undefined,
      titleField: "display_name",
      secondaryFields: [],
    });
  });
});

describe("bucketRowsByGroup", () => {
  it("buckets rows by the group_by field's string value, preserving row order within each bucket", () => {
    const rows: Row[] = [
      { id: "1", stage: "new" },
      { id: "2", stage: "won" },
      { id: "3", stage: "new" },
    ];
    const buckets = bucketRowsByGroup(rows, "stage");
    expect(buckets.get("new")).toEqual([rows[0], rows[2]]);
    expect(buckets.get("won")).toEqual([rows[1]]);
  });

  it("buckets a missing group_by value under the empty-string key", () => {
    const rows: Row[] = [{ id: "1" }];
    expect(bucketRowsByGroup(rows, "stage").get("")).toEqual(rows);
  });
});

describe("deriveGroupIds", () => {
  it("uses group_values verbatim, in the declared order, when given", () => {
    const rows: Row[] = [{ id: "1", stage: "won" }];
    expect(deriveGroupIds(rows, "stage", ["new", "qualified", "won"])).toEqual(["new", "qualified", "won"]);
  });

  it("derives the column set and order from first appearance in the data when group_values is omitted", () => {
    const rows: Row[] = [
      { id: "1", stage: "won" },
      { id: "2", stage: "new" },
      { id: "3", stage: "won" },
    ];
    expect(deriveGroupIds(rows, "stage", undefined)).toEqual(["won", "new"]);
  });

  it("returns no columns for an empty result set with no group_values declared", () => {
    expect(deriveGroupIds([], "stage", undefined)).toEqual([]);
  });
});
