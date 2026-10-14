import { permissionRef } from "@goerp/sdk";
import { renderView } from "@goerp/shell-app/testing";
import type { Contact } from "../../../../internal/codegen/testdata/examplemod.generated.js";

declare const contact: Contact;

void renderView("contacts.contacts_list", {
  api: { listContacts: async () => ({ data: [contact], meta: { cursor: null, hasMore: false } }) },
  permissions: ["contacts:contact:read", permissionRef("hr:employee:read")],
});

// @ts-expect-error View names must name a module with a generated API.
void renderView("missing.contacts_list");
void renderView("contacts.contacts_list", {
  // @ts-expect-error Only generated API functions are accepted.
  api: { missing: async () => ({}) },
});
void renderView("contacts.contacts_list", {
  // @ts-expect-error List mocks return complete generated records.
  api: { listContacts: async () => ({ data: [{ id: "contact-1" }], meta: { cursor: null, hasMore: false } }) },
});
void renderView("contacts.contacts_list", {
  // @ts-expect-error Mock parameters retain their generated signature.
  api: { getContact: async (_id: number) => contact },
});
void renderView("contacts.contacts_list", {
  // @ts-expect-error Foreign permissions require permissionRef.
  permissions: ["hr:employee:read"],
});
