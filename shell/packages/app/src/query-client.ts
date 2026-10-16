import { isAppError } from "@goerp/sdk/error";
import { QueryClient } from "@tanstack/react-query";
import { configureViewPrefetch } from "./renderers/prefetch-view-data.js";

const MAX_RETRIES = 3;

// A client error such as 403 or 404 repeats on every attempt, so only server and network
// failures are retried.
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (isAppError(error) && error.httpStatus < 500) return false;
  return failureCount < MAX_RETRIES;
}

export function createQueryClient(): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
        retry: shouldRetryQuery,
      },
    },
  });
  configureViewPrefetch(queryClient);
  return queryClient;
}
