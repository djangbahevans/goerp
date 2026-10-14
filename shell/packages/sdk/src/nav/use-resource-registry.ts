import { useContext, useMemo } from "react";
import type { ResourceName } from "../names.js";
import type { ResourceMetadataEntry } from "../schema/resource-metadata-registry.js";
import { ViewRegistryContext } from "../schema/view-registry-provider.js";

export interface ResourceRegistry {
  get(resource: ResourceName): ResourceMetadataEntry | undefined;
}

export function useResourceRegistry(): ResourceRegistry {
  const registry = useContext(ViewRegistryContext);
  return useMemo(() => ({ get: (resource: ResourceName) => registry?.resourceMetadata.get(resource) }), [registry]);
}
