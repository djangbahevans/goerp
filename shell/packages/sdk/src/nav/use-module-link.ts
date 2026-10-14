import { useCallback, useContext } from "react";
import { PermissionContext } from "../auth/permission-provider.js";
import type { ResourceName } from "../names.js";
import { useNavigate } from "../react/use-module.js";
import { ViewRegistryContext } from "../schema/view-registry-provider.js";
import { moduleLink } from "./module-link.js";
import { useResourceRegistry } from "./use-resource-registry.js";

export function useModuleLink(
  resource: ResourceName,
  id: string,
): {
  href: string | null;
  isAvailable: boolean;
  navigate: () => void;
} {
  const navigate = useNavigate();
  const registry = useContext(ViewRegistryContext);
  const permissions = useContext(PermissionContext);
  const entry = useResourceRegistry().get(resource);
  const route =
    entry?.defaultFormView && permissions?.moduleEnabled(entry.module)
      ? registry?.recordViewPath(entry.defaultFormView, entry.module)
      : null;
  const path = route ? route.replaceAll("{id}", encodeURIComponent(id)) : null;
  const open = useCallback(() => {
    if (path) navigate(path);
  }, [path, navigate]);
  return { href: path ? moduleLink(path) : null, isAvailable: path !== null, navigate: open };
}
