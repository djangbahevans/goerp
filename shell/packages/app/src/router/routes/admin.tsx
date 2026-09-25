import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { ADMIN_NAV_GROUPS } from "../../admin/admin-nav.js";
import { isTenantAdmin } from "../../admin/is-tenant-admin.js";
import { SectionNav } from "../../chrome/section-nav.js";

// shell-ux.md §5. A session still being checked has no user yet and decides
// nothing; the root gate has already sent a signed-out visitor to sign in.
export const Route = createFileRoute("/admin")({
  beforeLoad: ({ context }) => {
    const { user } = context.auth;
    if (user && !isTenantAdmin(user)) throw redirect({ to: "/403", search: { reason: "missing_permission" } });
  },
  component: AdminLayout,
});

function AdminLayout() {
  return (
    <SectionNav label="Administration" groups={ADMIN_NAV_GROUPS}>
      <Outlet />
    </SectionNav>
  );
}
