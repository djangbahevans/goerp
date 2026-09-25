import type { CatalogPermission } from "./admin-roles-api.js";
import {
  type FakeRole,
  type FakeRolesBackendOptions,
  installFakeAdminRolesBackend,
} from "./fake-admin-roles-backend.js";

const permission = (name: string, description: string, category: string): CatalogPermission => ({
  name,
  description,
  category,
  module: name.split(":")[0] ?? "",
});

export const STORY_CATALOG: CatalogPermission[] = [
  permission("contacts:contact:delete", "Archive contacts", "Contacts"),
  permission("contacts:contact:financials_read", "View financial fields", "Contacts"),
  permission("contacts:contact:merge", "Merge duplicate contacts", "Contacts"),
  permission("contacts:contact:read", "View contacts", "Contacts"),
  permission("contacts:contact:write", "Create and edit contacts", "Contacts"),
  permission("sales:order:confirm", "Confirm sales orders", "Sales"),
  permission("sales:order:read", "View sales orders", "Sales"),
  permission("sales:order:write", "Create and edit sales orders", "Sales"),
  permission("sales:quote:read", "View quotes", "Sales"),
  permission("sales:quote:write", "Create and edit quotes", "Sales"),
];

export const STORY_ROLES: FakeRole[] = [
  {
    id: "r-admin",
    name: "admin",
    description: "All permissions within the organisation",
    isImmutable: true,
    userCount: 1,
    permissions: STORY_CATALOG.map((p) => p.name),
  },
  {
    id: "r-user",
    name: "user",
    description: "Basic read access",
    isImmutable: true,
    userCount: 3,
    permissions: ["contacts:contact:read", "sales:order:read"],
  },
  {
    id: "r-portal",
    name: "portal",
    description: "Customer and vendor portal",
    isImmutable: true,
    userCount: 0,
    permissions: [],
  },
  {
    id: "r-sales-rep",
    name: "sales_rep",
    description: "Creates quotes and orders for their accounts",
    isImmutable: false,
    userCount: 2,
    permissions: [
      "contacts:contact:read",
      "sales:order:read",
      "sales:order:write",
      "sales:quote:read",
      "sales:quote:write",
    ],
  },
  {
    id: "r-auditor",
    name: "auditor",
    description: null,
    isImmutable: false,
    userCount: 0,
    permissions: ["contacts:contact:financials_read", "contacts:contact:read", "payroll:run:read"],
  },
];

// A story's beforeEach: installs the in-memory backend and returns its cleanup.
export function fakeRolesBackend(options: Partial<FakeRolesBackendOptions> = {}) {
  return () => {
    const backend = installFakeAdminRolesBackend({ roles: STORY_ROLES, catalog: STORY_CATALOG, ...options });
    return backend.restore;
  };
}
