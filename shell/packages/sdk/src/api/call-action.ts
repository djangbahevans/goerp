import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { ActionName, ResultOf, VariablesArgs } from "../names.js";
import { type ActionRegistry, actionRegistry } from "../react/action-registry.js";
import { resolveActionResponse } from "../realtime/ui-executor.js";

export type DispatchClient = Pick<APIClient, "request">;

// Sends one action request and returns its result: a UI envelope's `data`
// after its instructions run, otherwise the body. Exported for callers
// dispatching dynamically named routes instead of one useAction() per route.
export async function dispatch<TResult>(
  client: DispatchClient,
  method: string,
  path: string,
  body: unknown,
): Promise<TResult> {
  // A GET action has no request body on the wire, so its variables become query params.
  const response =
    method === "GET"
      ? await client.request(
          method,
          path,
          undefined,
          body !== undefined ? { params: body as Record<string, unknown> } : undefined,
        )
      : await client.request(method, path, body);
  return resolveActionResponse<TResult>(method, response);
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
