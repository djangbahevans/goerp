import { apiClient } from "@goerp/sdk";
import { useMutation, useQueryClient } from "@tanstack/react-query";

// The config_schema entries and the save endpoint shared by the connector and
// module admin pages (shell-ux.md §5.4), mapped from their snake_case wire
// shapes.

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

export const MASKED = "***";

export interface EntryWire {
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

export function toEntry(wire: EntryWire): ConfigEntry {
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

export interface SaveConfigResult {
  updated: string[];
  restartRequired: string[];
  configured: boolean;
}

// Sends only the changed keys, qualified with the module name
// (PATCH /admin/config). A null value resets a key to its default. Once a
// save lands, every query under one of invalidate's keys is refetched.
export function useSaveModuleConfig(name: string, invalidate: readonly (readonly unknown[])[]) {
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
    onSuccess: () => Promise.all(invalidate.map((queryKey) => queryClient.invalidateQueries({ queryKey }))),
  });
}
