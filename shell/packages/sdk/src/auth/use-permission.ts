import { useContext } from "react";
import { PermissionContext } from "./permission-provider.js";

function usePermissionContext() {
  const value = useContext(PermissionContext);
  if (!value) {
    throw new Error("usePermission/useFieldPermission/Can must be used within a PermissionProvider");
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
