import { useContext } from "react";
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

export function usePermission(permission: string, resourceId?: string): boolean {
  const { check } = usePermissionContext();
  return check(permission, resourceId);
}

export function useFieldPermission(model: string, field: string): { canRead: boolean; canWrite: boolean } {
  const { checkField } = usePermissionContext();
  return {
    canRead: checkField(model, field, "read"),
    canWrite: checkField(model, field, "write"),
  };
}

// Same throw-if-missing-provider contract as usePermission, but treats an
// absent `permission` as always-allowed — the shape a manifest Action's
// optional `permission` field needs (ActionButton, ActionMenu, and any
// other permission-gated element that isn't itself a required check).
export function useOptionalPermission(permission: string | undefined, resourceId?: string): boolean {
  const { check } = usePermissionContext();
  return !permission || check(permission, resourceId);
}
