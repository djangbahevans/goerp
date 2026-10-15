package codegen

import "testing"

func TestFileGeneratedTypes(t *testing.T) {
	generated, err := Generate(testInput([]Model{
		{Name: "contracts.contract", LabelPlural: "Contracts", Ops: []string{"list", "get", "create", "update"}, Fields: []Field{
			{Name: "id", Kind: "uuid", PrimaryKey: true, Readonly: true},
			{Name: "signed_pdf_id", Kind: "file", Required: true, ReadKey: "signed_pdf"},
			{Name: "avatar_id", Kind: "file", ReadKey: "avatar"},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}

	typecheck(t, map[string][]byte{
		"generated.ts": generated,
		"usage.ts": []byte(`
import { contactsApi, type Contract, type ContractCreate } from './generated.js';
export function file(contract: Contract) {
 const name: string | undefined = contract.signed_pdf?.name;
 const expiry: string | null | undefined = contract.signed_pdf?.url_expires_at;
 const create: ContractCreate = {signed_pdf_id: 'f1', avatar_id: null};
 void contactsApi.createContract(create);
 void contactsApi.updateContract('c1', {avatar_id: null});
 // @ts-expect-error file writes require IDs
 void contactsApi.updateContract('c1', {signed_pdf_id: {id: 'f1'}});
 // @ts-expect-error file expansions are read-only
 void contactsApi.updateContract('c1', {signed_pdf: {id: 'f1'}});
 return [name, expiry];
}
`),
	})
}
