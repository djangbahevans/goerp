import type { SectionNavGroup } from "../chrome/section-nav.js";

// shell-ux.md §4: each settings page adds its entry here when it ships.
export const SETTINGS_NAV_GROUPS: SectionNavGroup[] = [
  {
    items: [
      { to: "/settings/profile", label: "Profile", icon: "user" },
      { to: "/settings/appearance", label: "Appearance", icon: "palette" },
    ],
  },
];
