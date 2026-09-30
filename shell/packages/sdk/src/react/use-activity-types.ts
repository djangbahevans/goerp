import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../http/index.js";

// typescript-sdk-reference.md's useActivityTypes and admin activity-types
// hooks — the tenant's scheduled-activity types (scheduled-activities.md
// §9), fetched once per session, plus the admin CRUD (shell-ux.md §5.10).

export interface ActivityType {
  key: string;
  label: string;
  icon: string;
  defaultSummary: string | null;
  defaultDueDays: number | null;
  archived: boolean;
}

export interface AdminActivityType {
  key: string;
  label: Record<string, string>;
  icon: string;
  defaultSummary: Record<string, string>;
  defaultDueDays: number | null;
  archived: boolean;
  usageCount: number;
}

export interface ActivityTypeInput {
  key: string;
  label: Record<string, string>;
  icon: string;
  defaultSummary?: Record<string, string>;
  defaultDueDays?: number;
}

export interface ActivityTypeChanges {
  label?: Record<string, string>;
  icon?: string;
  defaultSummary?: Record<string, string>;
  defaultDueDays?: number | null;
  archived?: boolean;
}

interface ActivityTypeWire {
  key: string;
  label: string;
  icon: string;
  default_summary: string | null;
  default_due_days: number | null;
  archived: boolean;
}

interface AdminActivityTypeWire {
  key: string;
  label: Record<string, string>;
  icon: string;
  default_summary: Record<string, string>;
  default_due_days: number | null;
  archived: boolean;
  usage_count: number;
}

function toActivityType(wire: ActivityTypeWire): ActivityType {
  return {
    key: wire.key,
    label: wire.label,
    icon: wire.icon,
    defaultSummary: wire.default_summary,
    defaultDueDays: wire.default_due_days,
    archived: wire.archived,
  };
}

function toAdminActivityType(wire: AdminActivityTypeWire): AdminActivityType {
  return {
    key: wire.key,
    label: wire.label,
    icon: wire.icon,
    defaultSummary: wire.default_summary,
    defaultDueDays: wire.default_due_days,
    archived: wire.archived,
    usageCount: wire.usage_count,
  };
}

const activityTypesListKey = ["activity-types"] as const;
const adminActivityTypesListKey = ["admin-activity-types"] as const;

export const activityTypesKey = activityTypesListKey;

export const adminActivityTypesKeys = {
  all: adminActivityTypesListKey,
  list: () => [...adminActivityTypesListKey, "list"] as const,
};

// GET /_meta/activity-types — every member of the tenant. Resolved for the
// caller's locale; the shell fetches this once per session.
export function useActivityTypes() {
  const query = useQuery({
    queryKey: activityTypesListKey,
    staleTime: Number.POSITIVE_INFINITY,
    queryFn: async ({ signal }) => {
      const { data } = await apiClient.get<{ data: ActivityTypeWire[] }>("/_meta/activity-types", { signal });
      return data.map(toActivityType);
    },
  });
  const types = query.data ?? [];
  return {
    types,
    isLoading: query.isLoading,
    isError: query.isError,
    getType: (key: string) => types.find((type) => type.key === key),
  };
}

export function useAdminActivityTypes() {
  return useQuery({
    queryKey: adminActivityTypesKeys.list(),
    queryFn: async ({ signal }) => {
      const { data } = await apiClient.get<{ data: AdminActivityTypeWire[] }>("/admin/activity-types", { signal });
      return data.map(toAdminActivityType);
    },
  });
}

// An admin change invalidates both the admin list and the resolved public
// list, so shell-ux.md §5.10's "no reload" requirement holds everywhere a
// type's label/icon is shown.
function useActivityTypeMutation<TInput, TResult>(mutationFn: (input: TInput) => Promise<TResult>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: adminActivityTypesKeys.all }),
        queryClient.invalidateQueries({ queryKey: activityTypesListKey }),
      ]),
  });
}

export function useCreateActivityType() {
  return useActivityTypeMutation(async (input: ActivityTypeInput) =>
    toAdminActivityType(
      await apiClient.post<AdminActivityTypeWire>("/admin/activity-types", {
        key: input.key,
        label: input.label,
        icon: input.icon,
        ...(input.defaultSummary ? { default_summary: input.defaultSummary } : {}),
        ...(input.defaultDueDays !== undefined ? { default_due_days: input.defaultDueDays } : {}),
      }),
    ),
  );
}

// Takes the key as part of the mutation input, not the hook call, so a list
// page can use one hook instance for every row instead of one per key.
export function useUpdateActivityType() {
  return useActivityTypeMutation(async ({ key, changes }: { key: string; changes: ActivityTypeChanges }) => {
    const body: Record<string, unknown> = {};
    if (changes.label !== undefined) body.label = changes.label;
    if (changes.icon !== undefined) body.icon = changes.icon;
    if (changes.defaultSummary !== undefined) body.default_summary = changes.defaultSummary;
    if (changes.defaultDueDays !== undefined) body.default_due_days = changes.defaultDueDays;
    if (changes.archived !== undefined) body.archived = changes.archived;
    return toAdminActivityType(await apiClient.patch<AdminActivityTypeWire>(`/admin/activity-types/${key}`, body));
  });
}

export function useReorderActivityTypes() {
  return useActivityTypeMutation(async (keys: string[]) => {
    const { data } = await apiClient.put<{ data: AdminActivityTypeWire[] }>("/admin/activity-types/order", { keys });
    return data.map(toAdminActivityType);
  });
}

export function useDeleteActivityType() {
  return useActivityTypeMutation<string, void>((key: string) => apiClient.delete<void>(`/admin/activity-types/${key}`));
}
