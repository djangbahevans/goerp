import { resourceRef } from "@goerp/sdk";
import { useModuleLink, useNavigate, useResourceRegistry } from "@goerp/sdk/nav";
import type { Contact } from "../../../../internal/codegen/testdata/examplemod.generated.js";

function NavigationTypes() {
  useModuleLink("contacts.contact", "c1");
  useModuleLink(resourceRef<Contact>("hr.employee"), "e1");
  useResourceRegistry().get("contacts.contact");
  useResourceRegistry().get(resourceRef("hr.employee"));
  useNavigate()("/settings/profile", { shell: true, replace: true });
  // @ts-expect-error Foreign resources require a resourceRef.
  useModuleLink("hr.employee", "e1");
  // @ts-expect-error Foreign resources require a resourceRef.
  useResourceRegistry().get("hr.employee");
}
void NavigationTypes;
