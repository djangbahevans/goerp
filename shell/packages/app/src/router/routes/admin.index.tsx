import { createFileRoute, redirect } from "@tanstack/react-router";

// shell-ux.md §1: "/admin → redirect to /admin/users".
export const Route = createFileRoute("/admin/")({
  beforeLoad: () => {
    throw redirect({ to: "/admin/users", replace: true });
  },
});
