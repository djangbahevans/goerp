import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useContext, useMemo } from "react";
import { PermissionContext } from "../auth/permission-provider.js";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import { useTenant } from "../auth/use-tenant.js";
import { useUser } from "../auth/use-user.js";
import type { ToastAPI } from "../notifications/toast.js";
import { useToast } from "./use-toast.js";

export interface NavigateOptions {
  // Replace the current history entry instead of pushing a new one.
  replace?: boolean | undefined;
}

// A shell path, optionally with a query string ("/contacts?stage=lead").
export type NavigateFn = (path: string, options?: NavigateOptions) => void;

// typescript-sdk-reference.md §5 "useModule". Further fields (t, realtime,
// api) join this interface as they are built.
export interface ModuleContext {
  user: CurrentUser;
  tenant: CurrentTenant;
  can: (permission: string, resourceId?: string) => boolean;
  navigate: NavigateFn;
  queryClient: QueryClient;
  toast: ToastAPI;
}

// The SDK has no router dependency, so the shell supplies a router-backed
// navigate around everything it renders.
const ModuleNavigationContext = createContext<NavigateFn | null>(null);

export interface ModuleNavigationProviderProps {
  navigate: NavigateFn;
  children: ReactNode;
}

export function ModuleNavigationProvider({ navigate, children }: ModuleNavigationProviderProps): ReactNode {
  return <ModuleNavigationContext.Provider value={navigate}>{children}</ModuleNavigationContext.Provider>;
}

export function useModule(moduleName: string): ModuleContext {
  const navigate = useContext(ModuleNavigationContext);
  const permissions = useContext(PermissionContext);
  if (!navigate || !permissions) {
    throw new Error(
      `useModule("${moduleName}") must be called inside a module view the shell renders: ` +
        `no ${navigate ? "PermissionProvider" : "ModuleNavigationProvider"} above it`,
    );
  }
  const user = useUser();
  const tenant = useTenant();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const { check } = permissions;

  return useMemo(
    () => ({ user, tenant, can: check, navigate, queryClient, toast }),
    [user, tenant, check, navigate, queryClient, toast],
  );
}
