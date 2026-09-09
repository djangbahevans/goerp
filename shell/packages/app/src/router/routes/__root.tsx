import { Toast } from "@goerp/sdk/components";
import { createRootRoute, Outlet } from "@tanstack/react-router";
import { CommandPalette } from "../../chrome/index.js";

export const Route = createRootRoute({
  component: () => (
    <>
      <Outlet />
      <Toast />
      <CommandPalette />
    </>
  ),
});
