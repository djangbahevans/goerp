import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useCallback, useContext, useMemo, useSyncExternalStore } from "react";
import { PermissionContext } from "../auth/permission-provider.js";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import { useTenant } from "../auth/use-tenant.js";
import { useUser } from "../auth/use-user.js";
import { moduleApiRegistry } from "../module/module-api-registry.js";
import type { ToastAPI } from "../notifications/toast.js";
import { type RealtimeAPI, realtime } from "../realtime/realtime-api.js";
import { useToast } from "./use-toast.js";

export interface NavigateOptions {
  // Replace the current history entry instead of pushing a new one.
  replace?: boolean | undefined;
}

// A shell path, optionally with a query string ("/contacts?stage=lead").
export type NavigateFn = (path: string, options?: NavigateOptions) => void;

// Each module's generated client file adds its module here through
// declaration merging (typescript-sdk-reference.md §4).
// biome-ignore lint/suspicious/noEmptyInterface: extended by declaration merging
export interface ModuleApis {}

// typescript-sdk-reference.md §5 "useModule". The t field joins this
// interface once it is built.
export interface ModuleContext<N extends string = string> {
  // undefined at runtime when the module registered no api.
  api: N extends keyof ModuleApis ? ModuleApis[N] : unknown;
  user: CurrentUser;
  tenant: CurrentTenant;
  can: (permission: string, resourceId?: string) => boolean;
  navigate: NavigateFn;
  queryClient: QueryClient;
  toast: ToastAPI;
  realtime: RealtimeAPI;
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

export function useModule<N extends string>(moduleName: N): ModuleContext<N> {
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
  const getApi = useCallback(() => moduleApiRegistry.resolve(moduleName), [moduleName]);
  const api = useSyncExternalStore(subscribeToModuleApis, getApi, getApi) as ModuleContext<N>["api"];

  return useMemo(
    () => ({ api, user, tenant, can: check, navigate, queryClient, toast, realtime }),
    [api, user, tenant, check, navigate, queryClient, toast],
  );
}

function subscribeToModuleApis(listener: () => void): () => void {
  return moduleApiRegistry.subscribe(listener);
}
