import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";

// typescript-sdk-reference.md's useShares — backed by the built-in
// /_meta/shares endpoint (view-system.md §12 "Document sharing"), not a
// module route. Failures reject with an AppError and show no toast, so the
// share panel can place each one where it applies.
export type SharePermission = "read" | "write";

export interface RecordShare {
  id: string;
  sharedWithUserId: string;
  sharedWithEmail: string | null;
  permission: SharePermission;
  expiresAt: string | null;
  createdAt: string;
}

export interface GrantShareInput {
  userEmail: string;
  permission: SharePermission;
  expiresAt?: string | undefined;
}

export interface UseSharesResult {
  shares: RecordShare[];
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;
  refetch: () => void;
  grant: (input: GrantShareInput) => Promise<void>;
  isGranting: boolean;
  revoke: (id: string) => Promise<void>;
  // Every share with a revoke in flight; concurrent revokes are each tracked.
  revokingIds: readonly string[];
}

interface ShareWire {
  id: string;
  shared_with_user_id: string;
  shared_with_email: string;
  permission: SharePermission;
  expires_at?: string | null;
  created_at: string;
}

function toRecordShare(wire: ShareWire): RecordShare {
  return {
    id: wire.id,
    sharedWithUserId: wire.shared_with_user_id,
    sharedWithEmail: wire.shared_with_email === "" ? null : wire.shared_with_email,
    permission: wire.permission,
    expiresAt: wire.expires_at ?? null,
    createdAt: wire.created_at,
  };
}

function sharesQueryKey(model: string, recordId: string) {
  return ["shares", model, recordId] as const;
}

export function createSharesQueryOptions(model: string, recordId: string, client: Pick<APIClient, "get"> = apiClient) {
  return {
    queryKey: sharesQueryKey(model, recordId),
    queryFn: async (): Promise<RecordShare[]> => {
      const { data } = await client.get<{ data: ShareWire[] }>("/_meta/shares", {
        params: { model, record_id: recordId },
      });
      return data.map(toRecordShare);
    },
  };
}

export function useShares(model: string, recordId: string): UseSharesResult {
  const queryClient = useQueryClient();
  const query = useQuery(createSharesQueryOptions(model, recordId));
  const [revokingIds, setRevokingIds] = useState<readonly string[]>([]);
  const refetchShares = () => queryClient.invalidateQueries({ queryKey: sharesQueryKey(model, recordId) });

  const grantMutation = useMutation({
    mutationFn: (input: GrantShareInput) =>
      apiClient.post<ShareWire>("/_meta/shares", {
        model,
        record_id: recordId,
        user_email: input.userEmail,
        permission: input.permission,
        ...(input.expiresAt !== undefined ? { expires_at: input.expiresAt } : {}),
      }),
    onSuccess: refetchShares,
  });

  const revokeMutation = useMutation({
    mutationFn: async (id: string) => {
      try {
        await apiClient.delete<void>(`/_meta/shares/${id}`);
      } catch (err) {
        // Already revoked elsewhere: the caller's intent is satisfied.
        if (!(err instanceof AppError && err.httpStatus === 404)) throw err;
      }
    },
    onSuccess: refetchShares,
  });

  return {
    shares: query.data ?? [],
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error as AppError | null,
    refetch: () => void query.refetch(),
    grant: async (input) => {
      await grantMutation.mutateAsync(input);
    },
    isGranting: grantMutation.isPending,
    revoke: async (id) => {
      setRevokingIds((current) => [...current, id]);
      try {
        await revokeMutation.mutateAsync(id);
      } finally {
        setRevokingIds((current) => current.filter((revoking) => revoking !== id));
      }
    },
    revokingIds,
  };
}
