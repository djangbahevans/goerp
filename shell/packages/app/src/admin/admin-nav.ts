import type { SectionNavGroup } from "../chrome/section-nav.js";

// shell-ux.md §5: each admin page adds its entry here when it ships.
export const ADMIN_NAV_GROUPS: SectionNavGroup[] = [
  {
    items: [
      { to: "/admin/users", label: "Users", icon: "users" },
      { to: "/admin/roles", label: "Roles", icon: "shield" },
      { to: "/admin/activity-types", label: "Activity types", icon: "calendar-check" },
      { to: "/admin/connectors", label: "Connectors", icon: "plug" },
      { to: "/admin/settings", label: "Settings", icon: "settings" },
      { to: "/admin/settings/notifications", label: "Notification templates", icon: "mail" },
    ],
  },
];
