import { apiClient } from "../http/index.js";
import type { APIClient } from "../http/types.js";
import type { FieldAccessMap, PermissionData } from "./permission-types.js";

interface MetaPermissionsResponseBody {
  permissions: string[];
  field_access: FieldAccessMap;
  modules_enabled: string[];
}

// Backs PermissionContext's initial load (auth-internals.md "The /_meta/permissions endpoint";
// modules_enabled is multitenancy-internals.md §8's addition to that response).
export async function fetchPermissions(client: Pick<APIClient, "get"> = apiClient): Promise<PermissionData> {
  const body = await client.get<MetaPermissionsResponseBody>("/_meta/permissions");
  return {
    permissions: new Set(body.permissions),
    fieldAccess: body.field_access,
    modulesEnabled: new Set(body.modules_enabled),
  };
}
