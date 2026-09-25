import type { CatalogPermission } from "./admin-roles-api.js";

// shell-ux.md §5.2 "Permission groups". A permission `{module}:{resource}:{action}`
// belongs to the group `{module}:{resource}`; every other action in a group
// depends on the group's `read`.

export interface MatrixPermission {
  name: string;
  description: string | null;
}

export interface MatrixGroup {
  key: string;
  permissions: MatrixPermission[];
}

export interface MatrixSection {
  title: string;
  groups: MatrixGroup[];
}

export const UNCATALOGUED_SECTION = "Other permissions";

const READ = "read";

function groupOf(name: string): string {
  const i = name.lastIndexOf(":");
  return i < 0 ? name : name.slice(0, i);
}

function isRead(name: string): boolean {
  return name.slice(name.lastIndexOf(":") + 1) === READ;
}

function readOf(name: string): string {
  return `${groupOf(name)}:${READ}`;
}

// Sections follow the catalog's order (by module, then name) and are titled
// by each permission's category. Permissions the role holds that the catalog
// no longer lists, such as a disabled module's, come last so they stay
// visible and removable.
export function buildMatrix(catalog: readonly CatalogPermission[], held: readonly string[]): MatrixSection[] {
  const sections = new Map<string, Map<string, MatrixPermission[]>>();
  const add = (title: string, permission: MatrixPermission) => {
    const groups = sections.get(title) ?? new Map<string, MatrixPermission[]>();
    sections.set(title, groups);
    const key = groupOf(permission.name);
    groups.set(key, [...(groups.get(key) ?? []), permission]);
  };
  const known = new Set<string>();
  for (const permission of catalog) {
    known.add(permission.name);
    add(permission.category || permission.module, { name: permission.name, description: permission.description });
  }
  for (const name of [...held].sort()) {
    if (!known.has(name)) add(UNCATALOGUED_SECTION, { name, description: null });
  }
  return [...sections].map(([title, groups]) => ({
    title,
    groups: [...groups].map(([key, permissions]) => ({ key, permissions })),
  }));
}

export function matrixNames(sections: readonly MatrixSection[]): Set<string> {
  return new Set(
    sections.flatMap((section) => section.groups.flatMap((group) => group.permissions.map((p) => p.name))),
  );
}

// Selecting a non-read permission also selects its group's read, when the
// matrix has one; deselecting a read deselects the rest of its group.
export function togglePermission(
  selected: ReadonlySet<string>,
  name: string,
  checked: boolean,
  available: ReadonlySet<string>,
): Set<string> {
  const next = new Set(selected);
  if (checked) {
    next.add(name);
    if (!isRead(name) && available.has(readOf(name))) next.add(readOf(name));
    return next;
  }
  next.delete(name);
  if (isRead(name)) {
    const group = groupOf(name);
    for (const other of selected) {
      if (groupOf(other) === group) next.delete(other);
    }
  }
  return next;
}
