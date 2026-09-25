import {
  type QueryClient,
  type QueryKey,
  type UseMutationOptions,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { callActionWith, type DispatchClient } from "../api/call-action.js";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import { moduleErrorHandlerRegistry } from "../module/module-error-handler-registry.js";
import type { ErrorHandler, ErrorHandlerContext } from "../module/module-types.js";
import { toast } from "../notifications/toast.js";
import { type ActionRegistry, actionRegistry, moduleNameOf } from "./action-registry.js";

export { dispatch, splitPathAndBody } from "../api/call-action.js";
export type { ErrorHandler, ErrorHandlerContext };

export interface ActionOptions<TResult, TVariables> {
  invalidates?: QueryKey[];
  onMutate?: (variables: TVariables) => unknown;
  onError?: (err: AppError, variables: TVariables, context: unknown) => void;
  onSuccess?: (data: TResult, variables: TVariables) => void;
  onSettled?: (data: TResult | undefined, err: AppError | null, variables: TVariables) => void;
  // Overrides the module's own defineModule().errorHandlers default for
  // this one call — explicit null suppresses even that default. Omitted
  // entirely (not present in the options object at all) falls back to the
  // route's module's registered handler, looked up by this error's exact
  // code then that module's "*" wildcard; providing a local onError skips
  // the module default entirely, the same "isn't handled by a local
  // onError callback" precedence typescript-sdk-reference.md documents.
  errorHandler?: ErrorHandler | null;
  successMessage?: string | ((data: TResult) => string);
}

export interface ActionResult<TResult, TVariables> {
  mutate: (variables: TVariables) => void;
  mutateAsync: (variables: TVariables) => Promise<TResult>;
  isPending: boolean;
  isError: boolean;
  error: AppError | null;
  data: TResult | undefined;
  reset: () => void;
}

export function createActionMutationOptions<TResult, TVariables>(
  routeName: string,
  options: ActionOptions<TResult, TVariables>,
  queryClient: QueryClient,
  registry: ActionRegistry = actionRegistry,
  client: DispatchClient = apiClient,
): UseMutationOptions<TResult, AppError, TVariables, unknown> {
  return {
    mutationFn: (variables: TVariables) => callActionWith<TResult>(registry, client, routeName, variables),
    onError: (err, variables, context) => {
      if (options.errorHandler !== undefined) {
        options.errorHandler?.(err, { queryClient, toast });
      } else if (options.onError === undefined) {
        const moduleName = moduleNameOf(routeName);
        const moduleHandler =
          moduleName !== undefined ? moduleErrorHandlerRegistry.resolve(moduleName, err.code) : undefined;
        moduleHandler?.(err, { queryClient, toast });
      }
      options.onError?.(err, variables, context);
    },
    onSuccess: (data, variables) => {
      options.onSuccess?.(data, variables);
      if (options.successMessage) {
        const message =
          typeof options.successMessage === "function" ? options.successMessage(data) : options.successMessage;
        toast.success(message);
      }
      for (const key of options.invalidates ?? []) {
        void queryClient.invalidateQueries({ queryKey: key });
      }
    },
    // exactOptionalPropertyTypes rejects an explicit `undefined` for
    // these optional callbacks, so they're only included when provided.
    ...(options.onMutate ? { onMutate: options.onMutate } : {}),
    ...(options.onSettled ? { onSettled: options.onSettled } : {}),
  };
}

export function useAction<TResult = unknown, TVariables = unknown>(
  routeName: string,
  options: ActionOptions<TResult, TVariables> = {},
): ActionResult<TResult, TVariables> {
  const queryClient = useQueryClient();
  const mutation = useMutation(createActionMutationOptions<TResult, TVariables>(routeName, options, queryClient));

  return {
    mutate: (variables: TVariables) => {
      mutation.mutate(variables);
    },
    mutateAsync: (variables: TVariables) => mutation.mutateAsync(variables),
    isPending: mutation.isPending,
    isError: mutation.isError,
    error: mutation.error,
    data: mutation.data,
    reset: mutation.reset,
  };
}
