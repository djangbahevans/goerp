import {
  type QueryClient,
  type QueryKey,
  type UseMutationOptions,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import { toast } from "../notifications/toast.js";
import { type ActionRegistry, actionRegistry } from "./action-registry.js";

// Scoped down from typescript-sdk-reference.md's full ErrorHandlerContext
// (navigate, toast, queryClient) — navigate has no hook to source it from
// yet (no useNavigate built), so it's omitted rather than faked.
export interface ActionErrorHandlerContext {
  queryClient: QueryClient;
}

export type ErrorHandler = (err: AppError, ctx: ActionErrorHandlerContext) => void;

export interface ActionOptions<TResult, TVariables> {
  invalidates?: QueryKey[];
  onMutate?: (variables: TVariables) => unknown;
  onError?: (err: AppError, variables: TVariables, context: unknown) => void;
  onSuccess?: (data: TResult, variables: TVariables) => void;
  onSettled?: (data: TResult | undefined, err: AppError | null, variables: TVariables) => void;
  // No defineModule.errorHandlers registry exists yet to override, so a
  // provided handler is simply called and an omitted/null one is not —
  // there is no "module default" this currently falls back to.
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

type DispatchClient = Pick<APIClient, "get" | "post" | "put" | "patch" | "delete">;

async function dispatch<TResult>(
  client: DispatchClient,
  method: string,
  path: string,
  body: unknown,
): Promise<TResult> {
  switch (method) {
    case "GET":
      // A GET action has no request body on the wire — a computed body
      // becomes query params instead of being silently dropped.
      return client.get<TResult>(path, body !== undefined ? { params: body as Record<string, unknown> } : undefined);
    case "POST":
      return client.post<TResult>(path, body);
    case "PUT":
      return client.put<TResult>(path, body);
    case "PATCH":
      return client.patch<TResult>(path, body);
    case "DELETE":
      // APIClient.delete has no body parameter (matches the documented
      // interface) — fail loudly rather than silently dropping one.
      if (body !== undefined) {
        throw new Error(`useAction: DELETE ${path} resolved a request body, but APIClient.delete cannot send one`);
      }
      return client.delete<TResult>(path);
    default:
      throw new Error(`useAction: unsupported method "${method}"`);
  }
}

// Fills a path's first {param} placeholder and derives the request body
// from variables. Matches shell-architecture.md/view-system.md's two
// documented call shapes: a bare scalar supplies the placeholder with no
// body (confirm.mutate(orderId)), while an object carrying that
// placeholder's key supplies it for the URL and its own `body` field (or
// its remaining keys, if there's no `body` field) becomes the payload
// (update.mutate({ id, body })). A path with no placeholder sends
// variables as the body unchanged.
//
// Response `ui` instructions (shell-architecture.md §12a "Integration
// with route actions") aren't inspected here — the executor they'd
// dispatch to doesn't exist anywhere in this repo yet.
export function splitPathAndBody(path: string, variables: unknown): { path: string; body: unknown } {
  const placeholderMatch = /\{(\w+)\}/.exec(path);
  if (!placeholderMatch) {
    return { path, body: variables };
  }
  const [placeholder, paramName] = placeholderMatch as unknown as [string, string];

  if (variables !== null && typeof variables === "object") {
    if (!(paramName in variables)) {
      throw new Error(
        `useAction: route ${path} needs a "${paramName}" variable, but the object passed to mutate() has none`,
      );
    }
    const { [paramName]: paramValue, ...rest } = variables as Record<string, unknown>;
    const body = "body" in rest ? rest.body : Object.keys(rest).length > 0 ? rest : undefined;
    return { path: path.replace(placeholder, String(paramValue)), body };
  }

  return { path: path.replace(placeholder, String(variables)), body: undefined };
}

export function createActionMutationOptions<TResult, TVariables>(
  routeName: string,
  options: ActionOptions<TResult, TVariables>,
  queryClient: QueryClient,
  registry: ActionRegistry = actionRegistry,
  client: DispatchClient = apiClient,
): UseMutationOptions<TResult, AppError, TVariables, unknown> {
  return {
    mutationFn: async (variables: TVariables) => {
      const route = await registry.resolve(routeName);
      const { path, body } = splitPathAndBody(route.path, variables);
      return dispatch<TResult>(client, route.method, path, body);
    },
    onError: (err, variables, context) => {
      options.errorHandler?.(err, { queryClient });
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
