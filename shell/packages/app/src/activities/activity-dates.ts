// Due dates are calendar dates ("YYYY-MM-DD") with no time of day
// (scheduled-activities.md §1), so every comparison here is between date
// strings, which sort chronologically.

export type DueGroup = "overdue" | "today" | "upcoming";

export const DUE_GROUPS: readonly DueGroup[] = ["overdue", "today", "upcoming"];

export const DUE_GROUP_LABELS: Record<DueGroup, string> = {
  overdue: "Overdue",
  today: "Today",
  upcoming: "Upcoming",
};

// Today's date in timeZone. An unrecognized zone falls back to UTC rather
// than failing the page.
export function todayIn(timeZone: string, now: Date = new Date()): string {
  const format = (zone: string) =>
    new Intl.DateTimeFormat("en-CA", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
  try {
    return format(timeZone);
  } catch {
    return format("UTC");
  }
}

export function addDays(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

export function dueGroup(dueDate: string, today: string): DueGroup {
  if (dueDate < today) return "overdue";
  if (dueDate === today) return "today";
  return "upcoming";
}

// Items grouped Overdue, Today, Upcoming, each keeping its input order;
// empty groups are left out.
export function groupByDue<T extends { dueDate: string }>(
  items: readonly T[],
  today: string,
): { group: DueGroup; items: T[] }[] {
  const byGroup = new Map<DueGroup, T[]>(DUE_GROUPS.map((g) => [g, []]));
  for (const item of items) byGroup.get(dueGroup(item.dueDate, today))?.push(item);
  return DUE_GROUPS.flatMap((group) => {
    const groupItems = byGroup.get(group) ?? [];
    return groupItems.length > 0 ? [{ group, items: groupItems }] : [];
  });
}

// "Today" and "Tomorrow", otherwise the date itself.
export function dueLabel(dueDate: string, today: string, locale?: string): string {
  if (dueDate === today) return "Today";
  if (dueDate === addDays(today, 1)) return "Tomorrow";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeZone: "UTC" }).format(
    new Date(`${dueDate}T00:00:00Z`),
  );
}
