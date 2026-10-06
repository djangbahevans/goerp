import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

// The tenant admin connector endpoints (shell-ux.md §5.4), mapped from their
// snake_case wire shapes.

export interface ProviderStanding {
  category: string;
  primary: boolean;
  canSetPrimary: boolean;
}

export interface ConnectorSummary {
  name: string;
  displayName: string;
  description: string;
  version: string;
  enabled: boolean;
  configured: boolean;
  hasStatusRoute: boolean;
  provider: ProviderStanding | null;
}

export interface ConfigOption {
  value: string;
  label: string;
}

// One config_schema entry with the tenant's value. An encrypted entry's value
// is MASKED while set and null otherwise: the server never sends plaintext.
export interface ConfigEntry {
  key: string;
  label: string;
  description: string;
  type: string;
  fieldType: string;
  category: string;
  required: boolean;
  options: ConfigOption[];
  min: number | string | null;
  max: number | string | null;
  encrypted: boolean;
  generated: boolean;
  restartRequired: boolean;
  default: unknown;
  isSet: boolean;
  value: unknown;
}

export interface ConnectorDetail extends ConnectorSummary {
  config: ConfigEntry[];
}

export const MASKED = "***";

interface SummaryWire {
  name: string;
  display_name: string;
  description?: string;
  version: string;
  enabled: boolean;
  configured: boolean;
  has_status_route: boolean;
  provider: { category: string; primary: boolean; can_set_primary: boolean } | null;
}

interface EntryWire {
  key: string;
  label: string;
  description?: string;
  type: string;
  field_type?: string;
  category?: string;
  required: boolean;
  options?: ConfigOption[];
  min?: number | string;
  max?: number | string;
  encrypted: boolean;
  generated: boolean;
  restart_required: boolean;
  default: unknown;
  is_set: boolean;
  value: unknown;
}

interface DetailWire extends SummaryWire {
  config: EntryWire[];
}

function toSummary(wire: SummaryWire): ConnectorSummary {
  return {
    name: wire.name,
    displayName: wire.display_name,
    description: wire.description ?? "",
    version: wire.version,
    enabled: wire.enabled,
    configured: wire.configured,
    hasStatusRoute: wire.has_status_route,
    provider: wire.provider && {
      category: wire.provider.category,
      primary: wire.provider.primary,
      canSetPrimary: wire.provider.can_set_primary,
    },
  };
}

function toEntry(wire: EntryWire): ConfigEntry {
  return {
    key: wire.key,
    label: wire.label,
    description: wire.description ?? "",
    type: wire.type,
    fieldType: wire.field_type ?? "",
    category: wire.category ?? "",
    required: wire.required,
    options: wire.options ?? [],
    min: wire.min ?? null,
    max: wire.max ?? null,
    encrypted: wire.encrypted,
    generated: wire.generated,
    restartRequired: wire.restart_required,
    default: wire.default,
    isSet: wire.is_set,
    value: wire.value,
  };
}

const connectorsKey = ["admin-connectors"] as const;

export const connectorKeys = {
  all: connectorsKey,
  list: () => [...connectorsKey, "list"] as const,
  detail: (name: string) => [...connectorsKey, "detail", name] as const,
};

export function useConnectors() {
  return useQuery({
    queryKey: connectorKeys.list(),
    queryFn: async ({ signal }) => {
      const { connectors } = await apiClient.get<{ connectors: SummaryWire[] }>("/admin/connectors", { signal });
      return connectors.map(toSummary);
    },
  });
}

export function useConnector(name: string) {
  return useQuery({
    queryKey: connectorKeys.detail(name),
    // A connector that isn't installed stays missing; retrying only delays the not-found page.
    retry: (failures, err) => !(err instanceof AppError && err.httpStatus === 404) && failures < 3,
    queryFn: async ({ signal }): Promise<ConnectorDetail> => {
      const wire = await apiClient.get<DetailWire>(`/admin/connectors/${name}`, { signal });
      return { ...toSummary(wire), config: wire.config.map(toEntry) };
    },
  });
}

export interface SaveConfigResult {
  updated: string[];
  restartRequired: string[];
  configured: boolean;
}

// Sends only the changed keys, qualified with the module name
// (PATCH /admin/config). A null value resets a key to its default.
export function useSaveConnectorConfig(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (changes: Record<string, unknown>): Promise<SaveConfigResult> => {
      const body = Object.fromEntries(Object.entries(changes).map(([key, value]) => [`${name}.${key}`, value]));
      const wire = await apiClient.patch<{ updated: string[]; restart_required?: string[]; configured?: boolean }>(
        "/admin/config",
        body,
      );
      return {
        updated: wire.updated,
        restartRequired: wire.restart_required ?? [],
        configured: wire.configured ?? true,
      };
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: connectorKeys.all }),
  });
}

export function useRotateConfigValue(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (key: string) =>
      apiClient.post<{ key: string; value: string }>(`/admin/connectors/${name}/config/${key}/rotate`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: connectorKeys.detail(name) }),
  });
}

export function useSetPrimaryConnector() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiClient.patch<{ module_name: string; category: string }>(`/admin/connectors/${name}/set-primary`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: connectorKeys.all }),
  });
}

// A connector's own GET /connectors/{name}/status response.
export type ConnectorStatus = Record<string, unknown>;

export function useTestConnector(name: string) {
  return useMutation({
    mutationFn: () => apiClient.get<ConnectorStatus>(`/connectors/${name}/status`),
  });
}
