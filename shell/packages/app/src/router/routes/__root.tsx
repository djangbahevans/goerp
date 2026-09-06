import { Toast } from "@goerp/sdk/components";
import { createRootRoute, Outlet } from "@tanstack/react-router";

export const Route = createRootRoute({
  component: () => (
    <>
      <Outlet />
      <Toast />
    </>
  ),
});
