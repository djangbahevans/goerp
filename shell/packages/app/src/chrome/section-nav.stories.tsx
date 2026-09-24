import { PageHeader, PageLayout } from "@goerp/sdk/components";
import type { Meta, StoryObj } from "@storybook/react-vite";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { expect, userEvent, within } from "storybook/test";
import { SETTINGS_NAV_GROUPS } from "../settings/settings-nav.js";
import { SectionNav, type SectionNavGroup, type SectionNavLayout } from "./section-nav.js";

// shell-ux.md §5's admin pages, grouped the way a headed multi-group rail
// would carry them.
const ADMIN_GROUPS: SectionNavGroup[] = [
  {
    heading: "Access",
    items: [
      { to: "/admin/users", label: "Users", icon: "users" },
      { to: "/admin/roles", label: "Roles", icon: "shield" },
    ],
  },
  {
    heading: "Platform",
    items: [
      { to: "/admin/modules", label: "Modules", icon: "package" },
      { to: "/admin/connectors", label: "Connectors", icon: "plug" },
      { to: "/admin/settings", label: "Tenant settings", icon: "building-2" },
      { to: "/admin/view-overrides", label: "View overrides", icon: "layout-template" },
    ],
  },
  {
    heading: "Notifications & reports",
    items: [
      { to: "/admin/settings/notifications", label: "Notification templates", icon: "mail" },
      { to: "/admin/reports/scheduled", label: "Scheduled reports", icon: "calendar-clock" },
    ],
  },
  { heading: "Account", items: [{ to: "/admin/billing", label: "Billing", icon: "credit-card" }] },
];

function Page() {
  return (
    <PageLayout>
      <PageHeader title="Page title" subtitle="The section's routed page renders here." />
      {Array.from({ length: 30 }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: static filler rows.
        <p key={i} className="text-sm text-text-secondary">
          Filler row {i + 1} — scroll to check the rail stays put.
        </p>
      ))}
    </PageLayout>
  );
}

function renderAt(label: string, groups: SectionNavGroup[], path: string, layout?: SectionNavLayout) {
  const rootRoute = createRootRoute({
    component: () => (
      <div className="h-screen overflow-auto bg-bg">
        <SectionNav label={label} groups={groups} layout={layout}>
          <Outlet />
        </SectionNav>
      </div>
    ),
  });
  const routes = groups
    .flatMap((group) => group.items)
    .map((item) => createRoute({ getParentRoute: () => rootRoute, path: item.to, component: Page }));
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  return <RouterProvider router={router} />;
}

const meta: Meta<typeof SectionNav> = {
  title: "Chrome/SectionNav",
  component: SectionNav,
  parameters: { layout: "fullscreen" },
};

export default meta;

type Story = StoryObj<typeof SectionNav>;

// The settings section as it ships: one unheaded group.
export const Settings: Story = {
  render: () => renderAt("Settings", SETTINGS_NAV_GROUPS, "/settings/profile", "wide"),
};

// Headed groups, as the admin section will use them, with "Users" active.
export const AdminGroups: Story = {
  render: () => renderAt("Administration", ADMIN_GROUPS, "/admin/users", "wide"),
};

// Below 768px: one horizontal row, no headings, bottom-edge active bar. The
// active item starts off-screen and is scrolled into view on mount.
export const Narrow: Story = {
  render: () => renderAt("Administration", ADMIN_GROUPS, "/admin/reports/scheduled", "narrow"),
};

// The layout follows the real viewport: resize the canvas across 768px.
export const FollowsViewport: Story = {
  render: () => renderAt("Administration", ADMIN_GROUPS, "/admin/modules"),
};

// Keyboard focus on an item shows the --shadow-focus ring.
export const FocusVisible: Story = {
  render: () => renderAt("Administration", ADMIN_GROUPS, "/admin/users", "wide"),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await canvas.findByRole("link", { name: "Users" });
    await userEvent.tab();
    await expect(canvas.getByRole("link", { name: "Users" })).toHaveFocus();
  },
};
