import { apiClient } from "@goerp/sdk";
import { useAuth } from "@goerp/sdk/auth";
import { tenantChannel, useChannelRefresh } from "@goerp/sdk/realtime";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";

export interface CronJob {
  name: string;
  label: string;
  description: string;
  schedule: string;
  queue: string;
  timeout_seconds: number;
  enabled_by_default: boolean;
  enabled: boolean;
  generation: string;
  effective_enabled: boolean;
  blocked_reason: "module_not_entitled" | "module_not_ready" | "module_disabled" | "job_disabled" | null;
  next_run_at: string | null;
  updated_at: string;
}

export interface CronJobs {
  module: string;
  entitled: boolean;
  module_enabled: boolean;
  module_ready: boolean;
  cron_jobs: CronJob[];
}

export const cronKeys = {
  module: (tenant: string, user: string, module: string) => ["admin-cron-jobs", tenant, user, module] as const,
};

export function useCronJobs(name: string) {
  const { tenant, user, isAuthenticated } = useAuth();
  const tenantID = tenant?.id ?? "";
  const userID = user?.id ?? "";
  const key = useMemo(() => cronKeys.module(tenantID, userID, name), [tenantID, userID, name]);
  const query = useQuery({
    queryKey: key,
    enabled: isAuthenticated && !!tenant && !!user?.roles.includes("admin"),
    refetchOnWindowFocus: "always",
    refetchOnReconnect: "always",
    retry: false,
    queryFn: ({ signal }) =>
      apiClient.get<CronJobs>(`/admin/modules/${encodeURIComponent(name)}/cron-jobs`, { signal }),
  });
  const refresh = useCallback(() => {
    void query.refetch();
  }, [query.refetch]);
  useChannelRefresh(isAuthenticated, tenantID && tenantChannel(tenantID), "modules.changed", refresh);
  useChannelRefresh(isAuthenticated, tenantID && tenantChannel(tenantID), "module.installed", refresh);
  useChannelRefresh(isAuthenticated, tenantID && tenantChannel(tenantID), "plan.changed", refresh);
  useChannelRefresh(isAuthenticated, tenantID && tenantChannel(tenantID), "schema.updated", refresh);
  return { query, key };
}

export function setCronEnabled(module: string, job: CronJob, enabled: boolean) {
  return apiClient.patch<CronJob>(
    `/admin/modules/${encodeURIComponent(module)}/cron-jobs/${encodeURIComponent(job.name)}`,
    { enabled, expected_generation: job.generation },
    { retryNetworkErrors: false },
  );
}
