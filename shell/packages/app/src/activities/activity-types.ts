import type { IconNameLike } from "@goerp/sdk/components";

// The built-in activity types' labels and icons (scheduled-activities.md
// §9 "Built-in types"). A key outside this table shows its key and a
// generic icon.
const ACTIVITY_TYPES: Record<string, { label: string; icon: IconNameLike }> = {
  call: { label: "Call", icon: "phone" },
  meeting: { label: "Meeting", icon: "users" },
  email: { label: "Email", icon: "mail" },
  todo: { label: "To-do", icon: "square-check" },
};

export function activityTypeDisplay(type: string): { label: string; icon: IconNameLike } {
  return ACTIVITY_TYPES[type] ?? { label: type, icon: "calendar-check" };
}
