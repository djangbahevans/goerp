import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { ActionName, ResultOf, VariablesArgs } from "../names.js";
import { type ActionRegistry, actionRegistry } from "../react/action-registry.js";

export type DispatchClient = Pick<APIClient, "get" | "post" | "put" | "patch" | "delete">;

// Exported for callers dispatching several dynamically-named routes at
// runtime instead of one static useAction() hook per route.
export async function dispatch<TResult>(
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
        throw new Error(`DELETE ${path} resolved a request body, but APIClient.delete cannot send one`);
      }
      return client.delete<TResult>(path);
    default:
      throw new Error(`unsupported method "${method}"`);
  }
}

// Record actions consume the path parameter separately from their body;
// scalar variables remain useful for dynamically addressed shell actions.
export function splitPathAndBody(path: string, variables: unknown): { path: string; body: unknown } {
  const placeholderMatch = /\{(\w+)\}/.exec(path);
  if (!placeholderMatch) {
    return { path, body: variables };
  }
  const [placeholder, paramName] = placeholderMatch as unknown as [string, string];

  if (variables !== null && typeof variables === "object") {
    if (!(paramName in variables)) {
      throw new Error(`route ${path} needs a "${paramName}" variable, but the variables object has none`);
    }
    const { [paramName]: paramValue, ...rest } = variables as Record<string, unknown>;
    const body = "body" in rest ? rest.body : Object.keys(rest).length > 0 ? rest : undefined;
    return { path: path.replace(placeholder, String(paramValue)), body };
  }

  if (variables === undefined) {
    throw new Error(`route ${path} needs a "${paramName}" variable, but none was passed`);
  }
  return { path: path.replace(placeholder, String(variables)), body: undefined };
}

// Resolves an engine.DefineAction route by name ("module.actionName") and sends
// variables to it: the non-hook call useAction and generated clients share.
export async function callActionWith<TResult>(
  registry: Pick<ActionRegistry, "resolve">,
  client: DispatchClient,
  routeName: string,
  variables: unknown,
): Promise<TResult> {
  const route = await registry.resolve(routeName);
  const { path, body } = splitPathAndBody(route.path, variables);
  return dispatch<TResult>(client, route.method, path, body);
}

export function callAction<A extends ActionName>(
  routeName: A,
  ...args: VariablesArgs<NoInfer<A>>
): Promise<ResultOf<A>> {
  return callActionWith<ResultOf<A>>(actionRegistry, apiClient, routeName, args[0]);
}
