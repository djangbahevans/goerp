import type { IconNameLike } from "@goerp/sdk/components";

// The built-in activity types' labels and icons (scheduled-activities.md
// §9 "Built-in types"). A key outside this table shows its key and a
// generic icon.
export const BUILT_IN_ACTIVITY_TYPES: readonly { key: string; label: string; icon: IconNameLike }[] = [
  { key: "call", label: "Call", icon: "phone" },
  { key: "meeting", label: "Meeting", icon: "users" },
  { key: "email", label: "Email", icon: "mail" },
  { key: "todo", label: "To-do", icon: "square-check" },
];

export function activityTypeDisplay(type: string): { label: string; icon: IconNameLike } {
  return BUILT_IN_ACTIVITY_TYPES.find((t) => t.key === type) ?? { label: type, icon: "calendar-check" };
}
