import { type PagedResponse, resourceRef } from "@goerp/sdk";
import { type ListOptions, type UseListResult, useList } from "@goerp/sdk/react";
import type { Contact } from "../../../../internal/codegen/testdata/examplemod.generated.js";

function ListTypes() {
  const contacts: UseListResult<"contacts.contact"> = useList("contacts.contact", {
    filter: { type: "person", is_active: true },
    sort: ["-created_at", "name"],
    limit: 25,
    cursor: "c2",
    enabled: true,
    staleTime: 1000,
    realtimeChannel: null,
  });
  const page: PagedResponse<Contact> | undefined = contacts.data;
  const name: string | undefined = contacts.data?.data[0]?.name;
  const cursor: string | undefined = contacts.cursor;
  const hasMore: boolean = contacts.hasMore;
  contacts.nextPage();
  contacts.prevPage();
  contacts.refetch();
  useList("contacts.contact", { sort: "email", realtimeChannel: "feed:contacts" });
  useList("contacts.contact", {
    select: (data) => ({ ...data, data: data.data.filter((contact) => contact.email !== null) }),
  });
  const options: ListOptions<"contacts.contact"> = { filter: { status: "lead" } };
  useList("contacts.contact", options);
  const ref = useList(resourceRef<Contact>("directory.person"), { filter: { email: "a@b.c" }, sort: "-name" });
  const refName: string | undefined = ref.data?.data[0]?.name;

  // @ts-expect-error Unknown resources require a resourceRef.
  useList("directory.person");
  // @ts-expect-error Filters must name a field of this resource.
  useList("contacts.contact", { filter: { naem: "Ada" } });
  // @ts-expect-error Sort keys must name a field of this resource.
  useList("contacts.contact", { sort: "-naem" });
  // @ts-expect-error Each sort key must name a field of this resource.
  useList("contacts.contact", { sort: ["name", "missing"] });
  // @ts-expect-error Branded resources retain their field names.
  useList(resourceRef<Contact>("directory.person"), { filter: { missing: 1 } });
  // @ts-expect-error The record type follows from the resource.
  const missing: string | undefined = contacts.data?.data[0]?.missing;
  // @ts-expect-error select keeps the paged record type.
  useList("contacts.contact", { select: (data) => data.data });
  void [page, name, cursor, hasMore, refName, missing];
}
void ListTypes;
