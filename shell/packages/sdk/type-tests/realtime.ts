import { resourceRef } from "@goerp/sdk";
import { type PresenceUser, type UsePresenceOptions, usePresence } from "@goerp/sdk/realtime";
import type { Contact } from "../../../../internal/codegen/testdata/examplemod.generated.js";

function PresenceTypes() {
  const users = usePresence("contacts.contact", "c1", { editingField: "name" });
  const field: (keyof Contact & string) | null | undefined = users[0]?.editingField;
  const date: Date | undefined = users[0]?.joinedAt;
  const options: UsePresenceOptions<"contacts.contact"> = { editingField: null };
  const typed: PresenceUser<"contacts.contact">[] = usePresence("contacts.contact", "c1", options);
  usePresence(resourceRef<Contact>("directory.person"), "p1", { editingField: "email" });
  // @ts-expect-error Unknown resources require a resourceRef.
  usePresence("directory.person", "p1");
  // @ts-expect-error Focus must name a field of this resource.
  usePresence("contacts.contact", "c1", { editingField: "missing" });
  // @ts-expect-error Branded resources retain their field names.
  usePresence(resourceRef<Contact>("directory.person"), "p1", { editingField: "missing" });
  // @ts-expect-error Presence fields exclude undeclared names.
  const missing: "missing" = users[0]?.editingField;
  void [field, date, typed, missing];
}
void PresenceTypes;
