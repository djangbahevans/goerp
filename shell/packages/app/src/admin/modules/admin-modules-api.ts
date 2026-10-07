import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ConfigEntry, type EntryWire, toEntry, useSaveModuleConfig } from "../config/config-api.js";

// The tenant admin module endpoints (shell-ux.md §5.3), mapped from their
// snake_case wire shapes.

export interface ModulePermission {
  name: string;
  description: string;
  category: string;
}

export interface ModuleSummary {
  name: string;
  displayName: string;
  description: string;
  version: string;
  type: string;
  // The plan includes the module.
  entitled: boolean;
  // Entitled and not disabled by the tenant.
  enabled: boolean;
  dependsOn: string[];
  permissions: ModulePermission[];
}

interface ModuleWire {
  name: string;
  display_name: string;
  description?: string;
  version: string;
  type: string;
  entitled: boolean;
  enabled: boolean;
  depends_on: string[];
  permissions: { name: string; description?: string; category?: string }[];
}

function toModule(wire: ModuleWire): ModuleSummary {
  return {
    name: wire.name,
    displayName: wire.display_name,
    description: wire.description ?? "",
    version: wire.version,
    type: wire.type,
    entitled: wire.entitled,
    enabled: wire.enabled,
    dependsOn: wire.depends_on,
    permissions: wire.permissions.map((p) => ({
      name: p.name,
      description: p.description ?? "",
      category: p.category ?? "",
    })),
  };
}

export const moduleKeys = {
  all: ["admin-modules"] as const,
  list: () => [...moduleKeys.all, "list"] as const,
  detail: (name: string) => [...moduleKeys.all, "detail", name] as const,
};

export function useModules() {
  return useQuery({
    queryKey: moduleKeys.list(),
    queryFn: async ({ signal }) => {
      const { modules } = await apiClient.get<{ modules: ModuleWire[] }>("/admin/modules", { signal });
      return modules.map(toModule);
    },
  });
}

// A module's config_schema entries with the tenant's values; an encrypted
// value is masked. Fetched only while enabled, so a module the plan does not
// include is not queried.
export function useModuleConfig(name: string, enabled: boolean) {
  return useQuery({
    queryKey: moduleKeys.detail(name),
    enabled,
    queryFn: async ({ signal }): Promise<ConfigEntry[]> => {
      const wire = await apiClient.get<ModuleWire & { config: EntryWire[] }>(`/admin/modules/${name}`, { signal });
      return wire.config.map(toEntry);
    },
  });
}

export function useSaveModuleSettings(name: string) {
  return useSaveModuleConfig(name, [moduleKeys.detail(name)]);
}

export function useSetModuleEnabled() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ name, enabled }: { name: string; enabled: boolean }) =>
      toModule(await apiClient.patch<ModuleWire>(`/admin/modules/${name}/settings`, { enabled })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: moduleKeys.all }),
  });
}

// The modules a 409 dependency conflict names, in the engine's
// error.details.modules.
export function conflictingModules(err: unknown): string[] {
  if (!(err instanceof AppError) || err.httpStatus !== 409) return [];
  const modules = err.details?.modules;
  return Array.isArray(modules) ? modules.filter((m): m is string => typeof m === "string") : [];
}
