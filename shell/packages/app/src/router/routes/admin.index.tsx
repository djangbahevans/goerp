import { createFileRoute, redirect } from "@tanstack/react-router";

// Typed as a plain string until the users page is in the route tree.
const ADMIN_USERS_PATH: string = "/admin/users";

// shell-ux.md §1: "/admin → redirect to /admin/users".
export const Route = createFileRoute("/admin/")({
  beforeLoad: () => {
    throw redirect({ to: ADMIN_USERS_PATH, replace: true });
  },
});
