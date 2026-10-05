import type { Row } from "../list/list-view-types.js";

const AVATAR_FIELD_NAME = "avatar_file_id";

export const MAX_CARD_FIELDS = 5;

export interface CardFieldSplit {
  avatarField: string | undefined;
  titleField: string | undefined;
  secondaryFields: string[];
}

// Only the first MAX_CARD_FIELDS entries are shown, avatar included.
// "avatar_file_id" is pulled out to the avatar slot regardless of position;
// of what's left, the first field is the title, the rest are secondary.
export function splitCardFields(cardFields: string[]): CardFieldSplit {
  const shown = cardFields.slice(0, MAX_CARD_FIELDS);
  const avatarField = shown.find((field) => field === AVATAR_FIELD_NAME);
  const remaining = shown.filter((field) => field !== AVATAR_FIELD_NAME);
  const [titleField, ...secondaryFields] = remaining;
  return { avatarField, titleField, secondaryFields };
}

// Buckets rows by group_by's string value, preserving row order.
export function bucketRowsByGroup(rows: Row[], groupBy: string): Map<string, Row[]> {
  const buckets = new Map<string, Row[]>();
  for (const row of rows) {
    const key = String(row[groupBy] ?? "");
    const bucket = buckets.get(key);
    if (bucket) bucket.push(row);
    else buckets.set(key, [row]);
  }
  return buckets;
}

// group_values, when declared, is the full ordered column set (a row whose
// value isn't in it has no column). Otherwise columns are derived from the
// data's own first-appearance order.
export function deriveGroupIds(rows: Row[], groupBy: string, groupValues: string[] | undefined): string[] {
  if (groupValues) return groupValues;
  const order: string[] = [];
  const seen = new Set<string>();
  for (const row of rows) {
    const key = String(row[groupBy] ?? "");
    if (!seen.has(key)) {
      seen.add(key);
      order.push(key);
    }
  }
  return order;
}
