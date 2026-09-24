import { createFileRoute, redirect } from "@tanstack/react-router";

// shell-architecture.md §6: "/settings → redirect to /settings/profile".
export const Route = createFileRoute("/settings/")({
  beforeLoad: () => {
    throw redirect({ to: "/settings/profile", replace: true });
  },
});
