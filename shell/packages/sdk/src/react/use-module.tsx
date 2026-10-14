import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useCallback, useContext, useMemo, useSyncExternalStore } from "react";
import { PermissionContext } from "../auth/permission-provider.js";
import type { CurrentTenant, CurrentUser } from "../auth/types.js";
import { useTenant } from "../auth/use-tenant.js";
import { useUser } from "../auth/use-user.js";
import { type TranslateFn, useTranslation } from "../i18n/use-translation.js";
import { moduleApiRegistry } from "../module/module-api-registry.js";
import type { ModuleApis, PermissionName } from "../names.js";
import { moduleLink } from "../nav/module-link.js";
import type { ToastAPI } from "../notifications/toast.js";
import { type RealtimeAPI, realtime } from "../realtime/realtime-api.js";
import { useToast } from "./use-toast.js";

export interface NavigateOptions {
  replace?: boolean | undefined;
  shell?: boolean | undefined;
}

// Paths address module routes unless shell is true; query strings are preserved.
export type NavigateFn = (path: string, options?: NavigateOptions) => void;

export interface ModuleContext<N extends string = string> {
  api: N extends keyof ModuleApis ? ModuleApis[N] : unknown;
  user: CurrentUser;
  tenant: CurrentTenant;
  can: (permission: PermissionName, resourceId?: string) => boolean;
  t: TranslateFn;
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
  const moduleNavigate = useCallback<NavigateFn>(
    (path, options) => navigate(options?.shell ? path : moduleLink(path), options),
    [navigate],
  );
  return <ModuleNavigationContext.Provider value={moduleNavigate}>{children}</ModuleNavigationContext.Provider>;
}

export function useNavigate(): NavigateFn {
  const navigate = useContext(ModuleNavigationContext);
  if (!navigate) throw new Error("useNavigate must be called inside a ModuleNavigationProvider");
  return navigate;
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
  const { t } = useTranslation(moduleName);

  return useMemo(
    () => ({ api, user, tenant, can: check, t, navigate, queryClient, toast, realtime }),
    [api, user, tenant, check, t, navigate, queryClient, toast],
  );
}

function subscribeToModuleApis(listener: () => void): () => void {
  return moduleApiRegistry.subscribe(listener);
}
