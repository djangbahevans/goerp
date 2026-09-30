import type { FakeActivityType, FakeActivityTypesBackendOptions } from "./fake-admin-activity-types-backend.js";
import { installFakeAdminActivityTypesBackend } from "./fake-admin-activity-types-backend.js";

export const STORY_ACTIVITY_TYPES: FakeActivityType[] = [
  { key: "call", label: { en: "Call" }, icon: "phone", usageCount: 12 },
  { key: "meeting", label: { en: "Meeting" }, icon: "users", usageCount: 8 },
  { key: "email", label: { en: "Email" }, icon: "mail", usageCount: 20 },
  { key: "todo", label: { en: "To-do" }, icon: "square-check", usageCount: 5 },
  {
    key: "site_visit",
    label: { en: "Site visit" },
    icon: "map-pin",
    defaultSummary: { en: "Visit the site" },
    defaultDueDays: 2,
    usageCount: 0,
  },
  { key: "renewal_call", label: { en: "Renewal call" }, icon: "phone-call", archived: true, usageCount: 3 },
];

// A story's beforeEach: installs the in-memory backend and returns its cleanup.
export function fakeActivityTypesBackend(options: Partial<FakeActivityTypesBackendOptions> = {}) {
  return () => {
    const backend = installFakeAdminActivityTypesBackend({ types: STORY_ACTIVITY_TYPES, ...options });
    return backend.restore;
  };
}
