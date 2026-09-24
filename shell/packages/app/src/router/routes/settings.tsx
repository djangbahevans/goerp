import { createFileRoute, Outlet } from "@tanstack/react-router";
import { SectionNav } from "../../chrome/section-nav.js";
import { SETTINGS_NAV_GROUPS } from "../../settings/settings-nav.js";

export const Route = createFileRoute("/settings")({
  component: SettingsLayout,
});

function SettingsLayout() {
  return (
    <SectionNav label="Settings" groups={SETTINGS_NAV_GROUPS}>
      <Outlet />
    </SectionNav>
  );
}
