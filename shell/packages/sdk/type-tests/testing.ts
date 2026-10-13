import { permissionRef } from "@goerp/sdk";
import { createMockApi, type ShellProviderProps } from "@goerp/sdk/testing";
import type { Contact, contactsApi } from "../../../../internal/codegen/testdata/examplemod.generated.js";

declare const contact: Contact;

const api: typeof contactsApi = createMockApi("contacts", {
  listContacts: async (params) => ({ data: [contact], meta: { cursor: null, hasMore: Boolean(params?.limit) } }),
});
void api.listContacts({ limit: 10 });

// @ts-expect-error A generated API accepts only declared modules.
createMockApi("unknown");
// @ts-expect-error Overrides accept only declared API functions.
createMockApi("contacts", { missing: async () => [] });
createMockApi("contacts", {
  // @ts-expect-error The generated list result requires complete Contact records.
  listContacts: async () => ({ data: [{ id: "c1" }], meta: { cursor: null, hasMore: false } }),
});
createMockApi("contacts", {
  // @ts-expect-error Contact names are strings.
  listContacts: async () => ({ data: [{ ...contact, name: 42 }], meta: { cursor: null, hasMore: false } }),
});
createMockApi("contacts", {
  // @ts-expect-error The override parameter must have the generated signature.
  listContacts: async (_params: number) => ({ data: [contact], meta: { cursor: null, hasMore: false } }),
});
// @ts-expect-error Generated function argument types remain checked on the mock.
void api.listContacts({ limit: "ten" });

const permissions: ShellProviderProps["permissions"] = ["contacts:contact:read", permissionRef("hr:employee:read")];
void permissions;
// @ts-expect-error Foreign permissions require a permissionRef.
const foreignPermissions: ShellProviderProps["permissions"] = ["hr:employee:read"];
void foreignPermissions;
