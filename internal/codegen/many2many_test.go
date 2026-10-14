package codegen

import (
	"strings"
	"testing"
)

func TestMany2ManyGeneratedTypes(t *testing.T) {
	in := testInput([]Model{
		{Name: "contacts.contact", LabelPlural: "Contacts", Ops: []string{"list", "get", "create", "update"}, Fields: []Field{
			{Name: "id", Kind: "uuid", PrimaryKey: true, Readonly: true},
			{Name: "tag_ids", Kind: "many2many", RelatedModel: "contacts.tag", ReadKey: "tags"},
		}},
		{Name: "contacts.tag", LabelPlural: "Tags", Fields: []Field{
			{Name: "id", Kind: "uuid", PrimaryKey: true},
			{Name: "name", Kind: "text", Required: true},
			{Name: "color", Kind: "text"},
		}},
	})
	generated, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"tag_ids?: string[];", "tags?: Partial<Tag>[] | null;"} {
		if !strings.Contains(string(generated), fragment) {
			t.Fatalf("missing %s in\n%s", fragment, generated)
		}
	}
	typecheck(t, map[string][]byte{
		"generated.ts": generated,
		"usage.ts": []byte(`
import { contactsApi, type Contact, type ContactCreate } from './generated.js';
export function tags(contact: Contact) {
 const name: string | undefined = contact.tags?.[0]?.name;
 const color: string | null | undefined = contact.tags?.[0]?.color;
 const create: ContactCreate = {tag_ids: ['t1']};
 void contactsApi.createContact(create);
 void contactsApi.updateContact('c1', {tag_ids: []});
 // @ts-expect-error relations accept ID arrays, not null
 void contactsApi.updateContact('c1', {tag_ids: null});
 // @ts-expect-error relations accept IDs, not expanded objects
 void contactsApi.updateContact('c1', {tag_ids: [{id: 't1'}]});
 // @ts-expect-error expansion keys are read-only
 void contactsApi.updateContact('c1', {tags: []});
 return [name, color];
}
`),
	})
}
