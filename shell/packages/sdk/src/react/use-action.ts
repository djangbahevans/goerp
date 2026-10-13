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
import type { ActionName, ResultOf, VariablesOf } from "../names.js";
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
  // null suppresses the module handler; a local onError also takes precedence.
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

export function useAction<A extends ActionName>(
  routeName: A,
  options: ActionOptions<ResultOf<NoInfer<A>>, VariablesOf<NoInfer<A>>> = {},
): ActionResult<ResultOf<A>, VariablesOf<A>> {
  type TResult = ResultOf<A>;
  type TVariables = VariablesOf<A>;
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
