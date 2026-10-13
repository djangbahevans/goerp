import { useContext } from "react";
import type { FieldOf, PermissionName, ResourceName } from "../names.js";
import { PermissionContext } from "./permission-provider.js";

function usePermissionContext() {
  const value = useContext(PermissionContext);
  if (!value) {
    throw new Error(
      "usePermission/useFieldPermission/useOptionalPermission/Can must be used within a PermissionProvider",
    );
  }
  return value;
}

export function usePermission(permission: PermissionName, resourceId?: string): boolean {
  const { check } = usePermissionContext();
  return check(permission, resourceId);
}

export function useFieldPermission<R extends ResourceName>(
  model: R,
  field: FieldOf<NoInfer<R>>,
): { canRead: boolean; canWrite: boolean } {
  const { checkField } = usePermissionContext();
  return {
    canRead: checkField(model, field, "read"),
    canWrite: checkField(model, field, "write"),
  };
}

export function useOptionalPermission(permission: PermissionName | undefined, resourceId?: string): boolean {
  const { check } = usePermissionContext();
  return !permission || check(permission, resourceId);
}
