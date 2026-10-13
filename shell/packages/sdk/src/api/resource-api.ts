import { type FilterParamValue, flattenFilterParams } from "../http/filter-params.js";
import { apiClient } from "../http/index.js";
import { type PagedResponseWire, toPagedResponse } from "../http/paged-response.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { CreateOf, FilterOf, RecordOf, ResourceName, ResourceRef, SortOf, UpdateOf } from "../names.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

type Registry = Pick<ResourceRegistry, "resolve">;

export interface ListParams<R extends ResourceName = ResourceName> {
  filter?: FilterOf<R>;
  sort?: SortOf<R> | SortOf<R>[];
  cursor?: string;
  limit?: number;
}

function fillId(path: string, id: string): string {
  return path.replace("{id}", id);
}

export async function listResource<T>(
  resource: string,
  params: ListParams<ResourceRef<Record<string, unknown>>> = {},
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
): Promise<PagedResponse<T>> {
  const entry = await registry.resolve(resource);
  const wire = await client.get<PagedResponseWire<T>>(entry.listPath, {
    params: {
      ...flattenFilterParams(params.filter as Record<string, FilterParamValue> | undefined),
      ...(params.sort !== undefined ? { sort: Array.isArray(params.sort) ? params.sort.join(",") : params.sort } : {}),
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
  body: unknown,
  registry: Registry = resourceRegistry,
  client: Pick<APIClient, "post"> = apiClient,
): Promise<T> {
  const entry = await registry.resolve(resource);
  if (entry.createPath === "") {
    throw new Error(`resource "${resource}" declares no create route`);
  }
  return client.post<T>(entry.createPath, body);
}

export async function updateResource<T>(
  resource: string,
  id: string,
  body: unknown,
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

export const resourceApi = {
  list: <R extends ResourceName>(resource: R, params?: ListParams<NoInfer<R>>): Promise<PagedResponse<RecordOf<R>>> =>
    listResource<RecordOf<R>>(resource, params),
  get: <R extends ResourceName>(resource: R, id: string): Promise<RecordOf<R>> =>
    getResource<RecordOf<R>>(resource, id),
  create: <R extends ResourceName>(resource: R, body: CreateOf<NoInfer<R>>): Promise<RecordOf<R>> =>
    createResource<RecordOf<R>>(resource, body),
  update: <R extends ResourceName>(
    resource: R,
    id: string,
    body: UpdateOf<NoInfer<R>>,
    expectedEtag?: string,
  ): Promise<RecordOf<R>> => updateResource<RecordOf<R>>(resource, id, body, expectedEtag),
  delete: (resource: ResourceName, id: string): Promise<void> => deleteResource(resource, id),
};
