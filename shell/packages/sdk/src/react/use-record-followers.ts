import { type QueryClient, type QueryKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { ActivityAuthor } from "./use-record-activity.js";

// typescript-sdk-reference.md's useRecordFollowers — a record's followers
// and the caller's own follow state, backed by /_meta/activity/followers
// (record-activity.md §8). Failures reject with an AppError and show no
// toast, like useRecordActivity's mutations.
export interface RecordFollower {
  user: ActivityAuthor;
  createdAt: string;
}

export interface RecordFollowers {
  followers: RecordFollower[];
  isFollowing: boolean;
}

export interface UseRecordFollowersResult extends RecordFollowers {
  isLoading: boolean;
  isError: boolean;
  error: AppError | null;
  refetch: () => void;
  follow: () => Promise<void>;
  unfollow: () => Promise<void>;
  // A follow or unfollow is in flight.
  isUpdating: boolean;
}

interface RecordFollowersWire {
  data: { user: { id: string; name: string | null; avatar_url: string | null }; created_at: string }[];
  meta: { following: boolean };
}

export function recordFollowersQueryKey(model: string, recordId: string): QueryKey {
  return ["record-followers", model, recordId];
}

export function createRecordFollowersQueryOptions(
  model: string,
  recordId: string,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: recordFollowersQueryKey(model, recordId),
    queryFn: async (): Promise<RecordFollowers> => {
      const wire = await client.get<RecordFollowersWire>("/_meta/activity/followers", {
        params: { model, record_id: recordId },
      });
      return {
        followers: wire.data.map((f) => ({
          user: { id: f.user.id, name: f.user.name, avatarUrl: f.user.avatar_url },
          createdAt: f.created_at,
        })),
        isFollowing: wire.meta.following,
      };
    },
  };
}

export function createFollowMutationOptions(
  queryClient: QueryClient,
  model: string,
  recordId: string,
  client: Pick<APIClient, "put" | "delete"> = apiClient,
) {
  const body = { model, record_id: recordId };
  return {
    mutationFn: async (following: boolean): Promise<void> => {
      if (following) await client.put<void>("/_meta/activity/followers", body);
      else await client.delete<void>("/_meta/activity/followers", { body });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: recordFollowersQueryKey(model, recordId) }),
  };
}

export function useRecordFollowers(model: string, recordId: string): UseRecordFollowersResult {
  const queryClient = useQueryClient();
  const query = useQuery<RecordFollowers, AppError>(createRecordFollowersQueryOptions(model, recordId));
  const mutation = useMutation(createFollowMutationOptions(queryClient, model, recordId));

  return {
    followers: query.data?.followers ?? [],
    isFollowing: query.data?.isFollowing ?? false,
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    refetch: () => void query.refetch(),
    follow: () => mutation.mutateAsync(true),
    unfollow: () => mutation.mutateAsync(false),
    isUpdating: mutation.isPending,
  };
}
