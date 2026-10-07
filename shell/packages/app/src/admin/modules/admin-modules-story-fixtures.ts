import {
  type FakeModule,
  type FakeModulesBackendOptions,
  installFakeAdminModulesBackend,
} from "./fake-admin-modules-backend.js";

// Shared by the module stories and route tests.
export const CONTACTS: FakeModule = {
  name: "contacts",
  displayName: "Contacts",
  description: "People and companies the business interacts with.",
  version: "0.1.0",
  permissions: [
    { name: "contacts:contact:read", description: "View contacts", category: "Contacts" },
    { name: "contacts:contact:write", description: "Create and edit contacts", category: "Contacts" },
  ],
};

export const SALES: FakeModule = {
  name: "sales",
  displayName: "Sales",
  description: "Quotes, orders and invoices.",
  version: "1.4.2",
  dependsOn: ["contacts"],
  permissions: [{ name: "sales:order:read", description: "View sales orders", category: "Sales" }],
};

export const HR: FakeModule = {
  name: "hr",
  displayName: "HR",
  description: "Leave, payroll and people records.",
  version: "2.0.0",
  disabled: true,
};

export const PAYROLL_PLUS: FakeModule = {
  name: "payroll_plus",
  displayName: "Payroll Plus",
  description: "Advanced payroll.",
  version: "1.0.0",
  entitled: false,
};

export const STORY_MODULES: FakeModule[] = [CONTACTS, SALES, HR, PAYROLL_PLUS];

// A story's beforeEach: installs the in-memory backend and returns its cleanup.
export function fakeModulesBackend(options: Partial<FakeModulesBackendOptions> = {}) {
  return () => {
    const backend = installFakeAdminModulesBackend({ modules: STORY_MODULES, ...options });
    return backend.restore;
  };
}
