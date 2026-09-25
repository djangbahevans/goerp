import { type FilterParamValue, flattenFilterParams } from "../http/filter-params.js";
import { apiClient } from "../http/index.js";
import { type PagedResponseWire, toPagedResponse } from "../http/paged-response.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

type Registry = Pick<ResourceRegistry, "resolve">;

// The same filter, sort, cursor and limit parameters useInfiniteList takes.
export interface ListParams<T> {
  filter?: Partial<Record<keyof T & string, FilterParamValue>>;
  sort?: string;
  cursor?: string;
  limit?: number;
}

function fillId(path: string, id: string): string {
  return path.replace("{id}", id);
}

export async function listResource<T>(
  resource: string,
  params: ListParams<T> = {},
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
): Promise<PagedResponse<T>> {
  const entry = await registry.resolve(resource);
  const wire = await client.get<PagedResponseWire<T>>(entry.listPath, {
    params: {
      ...flattenFilterParams(params.filter as Record<string, FilterParamValue> | undefined),
      ...(params.sort !== undefined ? { sort: params.sort } : {}),
      ...(params.limit !== undefined ? { limit: params.limit } : {}),
      ...(params.cursor !== undefined ? { cursor: params.cursor } : {}),
    },
  });
  return toPagedResponse(wire);
}

export async function getResource<T>(
  resource: string,
  id: string,
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
): Promise<T> {
  const entry = await registry.resolve(resource);
  return client.get<T>(fillId(entry.getPath, id));
}

// Rejects rather than silently POSTing to an empty path when the model
// declares no create route (buildResourceRegistry's "" default).
export async function createResource<T>(
  resource: string,
  body: object,
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "post"> = apiClient,
): Promise<T> {
  const entry = await registry.resolve(resource);
  if (entry.createPath === "") {
    throw new Error(`resource "${resource}" declares no create route`);
  }
  return client.post<T>(entry.createPath, body);
}

// Sends the model's own declared update method (PUT/PATCH), with
// expectedEtag as If-Match when given.
export async function updateResource<T>(
  resource: string,
  id: string,
  body: object,
  expectedEtag?: string,
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "put" | "patch"> = apiClient,
): Promise<T> {
  const entry = await registry.resolve(resource);
  if (entry.updatePath === "") {
    throw new Error(`resource "${resource}" declares no update route`);
  }
  const path = fillId(entry.updatePath, id);
  const send = entry.updateMethod === "PATCH" ? client.patch.bind(client) : client.put.bind(client);
  return expectedEtag === undefined
    ? send<T>(path, body)
    : send<T>(path, body, { headers: { "If-Match": expectedEtag } });
}

export async function deleteResource(
  resource: string,
  id: string,
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "delete"> = apiClient,
): Promise<void> {
  const entry = await registry.resolve(resource);
  if (entry.deletePath === null) {
    throw new Error(`resource "${resource}" declares no delete route`);
  }
  await client.delete(fillId(entry.deletePath, id));
}

// CRUD on a model, with paths resolved through the resource registry
// (typescript-sdk-reference.md §4 "HTTP client").
export const resourceApi = {
  list: <T>(resource: string, params?: ListParams<T>): Promise<PagedResponse<T>> => listResource<T>(resource, params),
  get: <T>(resource: string, id: string): Promise<T> => getResource<T>(resource, id),
  create: <T>(resource: string, body: object): Promise<T> => createResource<T>(resource, body),
  update: <T>(resource: string, id: string, body: object, expectedEtag?: string): Promise<T> =>
    updateResource<T>(resource, id, body, expectedEtag),
  delete: (resource: string, id: string): Promise<void> => deleteResource(resource, id),
};
