export interface FieldAccess {
  read: boolean;
  write: boolean;
}

// Keys are model names ("contacts.contact"), not table names —
// auth-internals.md's field_access naming note.
export type FieldAccessMap = Record<string, Record<string, FieldAccess>>;

export interface PermissionData {
  permissions: Set<string>;
  // Only fields with a declared .Access() rule have an entry; any other field is unrestricted.
  fieldAccess: FieldAccessMap;
  modulesEnabled: Set<string>;
  // True when no /_meta/permissions response backs this data (still loading, or the fetch
  // failed), so every field is denied rather than unrestricted.
  pending?: boolean;
}

export interface PermissionContextValue extends PermissionData {
  check: (permission: string, resourceId?: string) => boolean;
  checkField: (model: string, field: string, mode: "read" | "write") => boolean;
  moduleEnabled: (moduleName: string) => boolean;
}
