import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ConfigEntry, type EntryWire, toEntry, useSaveModuleConfig } from "../config/config-api.js";

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

export interface ConnectorDetail extends ConnectorSummary {
  // The inbound webhook endpoint's path, once the first complete save minted
  // it and until it is revoked.
  webhookPath: string | null;
  config: ConfigEntry[];
}

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

interface DetailWire extends SummaryWire {
  webhook_path?: string;
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
      return { ...toSummary(wire), webhookPath: wire.webhook_path ?? null, config: wire.config.map(toEntry) };
    },
  });
}

export function useSaveConnectorConfig(name: string) {
  return useSaveModuleConfig(name, [connectorKeys.all]);
}

export function useRotateConfigValue(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (key: string) =>
      apiClient.post<{ key: string; value: string }>(`/admin/connectors/${name}/config/${key}/rotate`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: connectorKeys.detail(name) }),
  });
}

export function useRevokeWebhookEndpoint(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.delete<void>(`/admin/connectors/${name}/webhook`),
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
