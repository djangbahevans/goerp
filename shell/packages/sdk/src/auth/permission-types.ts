export interface FieldAccess {
  read: boolean;
  write: boolean;
}

// Keys are model names ("contacts.contact"), not table names —
// auth-internals.md's field_access naming note.
export type FieldAccessMap = Record<string, Record<string, FieldAccess>>;

export interface PermissionData {
  permissions: Set<string>;
  fieldAccess: FieldAccessMap;
  modulesEnabled: Set<string>;
}

export interface PermissionContextValue extends PermissionData {
  check: (permission: string, resourceId?: string) => boolean;
  checkField: (model: string, field: string, mode: "read" | "write") => boolean;
  moduleEnabled: (moduleName: string) => boolean;
}
