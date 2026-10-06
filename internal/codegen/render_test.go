package codegen

import (
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func contactModel(ops ...string) Model {
	return Model{
		Name:        "contacts.contact",
		LabelPlural: "Contacts",
		Fields: []Field{
			{Name: "id", Kind: "uuid", PrimaryKey: true, HasDefault: true, Readonly: true},
			{Name: "name", Kind: "text", Required: true},
		},
		Ops: ops,
	}
}

func testInput(models []Model, routes ...Route) *Input {
	in := &Input{Module: "contacts", PathPrefix: "/contacts", Models: models, Routes: routes}
	in.Catalog = map[string]*CatalogModule{"contacts": catalogFor(models, routes)}
	return in
}

func generateString(t *testing.T, in *Input) string {
	t.Helper()
	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	return string(out)
}

func TestFieldTSType_EveryKind(t *testing.T) {
	tests := []struct {
		kind   string
		values []string
		want   string
	}{
		{"char", nil, "string"},
		{"text", nil, "string"},
		{"uuid", nil, "string"},
		{"integer", nil, "number"},
		{"bigint", nil, "number"},
		{"float", nil, "number"},
		{"decimal", nil, "string"},
		{"boolean", nil, "boolean"},
		{"timestamptz", nil, "string"},
		{"date", nil, "string"},
		{"time", nil, "string"},
		{"jsonb", nil, "unknown"},
		{"bytea", nil, "string"},
		{"sequence", nil, "string"},
		{"many2one", nil, "string"},
		{"dynamic_link", nil, "string"},
		{"selection", []string{"a", "it's"}, `'a' | 'it\'s'`},
		{"enum", []string{"s", "m"}, "'s' | 'm'"},
		{"selection", nil, "string"},
	}
	for _, tt := range tests {
		got, ok := FieldTSType(tt.kind, tt.values)
		if !ok || got != tt.want {
			t.Errorf("FieldTSType(%q, %v) = %q, %v; want %q, true", tt.kind, tt.values, got, ok, tt.want)
		}
	}
	if _, ok := FieldTSType("one2many", nil); ok {
		t.Error("FieldTSType(one2many) ok = true, want false: a record carries no one2many value")
	}
}

func TestGenerate_NullabilityAndCreateOptionality(t *testing.T) {
	m := contactModel("create")
	m.Fields = append(m.Fields,
		Field{Name: "email", Kind: "char"},
		Field{Name: "is_active", Kind: "boolean", Required: true, HasDefault: true},
		Field{Name: "total", Kind: "decimal", Readonly: true},
		Field{Name: "lines", Kind: "one2many"},
		Field{Name: "billing-code", Kind: "text"},
		Field{Name: "number", Kind: "sequence", Required: true},
	)
	out := generateString(t, testInput([]Model{m}))

	for _, want := range []string{
		"  id: string;\n",
		"  email: string | null;\n",
		"  is_active: boolean;\n",
		"  total: string | null;\n",
		"  'billing-code': string | null;\n",
		"export type ContactCreate = {\n  name: string;\n  email?: string | null;\n  is_active?: boolean;\n  'billing-code'?: string | null;\n  number?: string;\n};",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "lines") {
		t.Errorf("output includes the one2many field:\n%s", out)
	}
	if strings.Contains(out, "ContactUpdate") {
		t.Errorf("output declares ContactUpdate for a model with no update op:\n%s", out)
	}
}

func TestGenerate_DuplicateFunctionNameNamesBothRoutes(t *testing.T) {
	in := testInput([]Model{contactModel()},
		Route{Method: "GET", Path: "/by-email"},
		Route{Method: "GET", Path: "/by_email"},
	)
	_, err := Generate(in)
	if err == nil {
		t.Fatal("Generate() error = nil, want a duplicate-name error")
	}
	for _, want := range []string{"GET /by-email", "GET /by_email", `"getByEmail"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestGenerate_ActionCollidingWithCRUDFunctionFails(t *testing.T) {
	// A custom action named "list" on a model without the list op derives
	// listContact; a second model whose plural is "Contact" would derive
	// the same name from EnableOps list.
	odd := Model{Name: "contacts.contactx", LabelPlural: "Contact", Ops: []string{"list"}}
	in := testInput([]Model{contactModel(), odd},
		Route{Model: "contacts.contact", Name: "list", CRUDAction: "list", Scope: CollectionScope})
	if _, err := Generate(in); err == nil || !strings.Contains(err.Error(), `"listContact"`) {
		t.Fatalf("Generate() error = %v, want a listContact collision", err)
	}
}

func TestGenerate_TwoGoTypesWithOneNameFail(t *testing.T) {
	a := &abiv1.TypeDesc{Kind: abiv1.TypeKindObject, Name: "Result", Fields: []abiv1.FieldDesc{{Name: "ok", Type: abiv1.TypeDesc{Kind: abiv1.TypeKindBoolean}}}}
	b := &abiv1.TypeDesc{Kind: abiv1.TypeKindObject, Name: "Result", Fields: []abiv1.FieldDesc{{Name: "count", Type: abiv1.TypeDesc{Kind: abiv1.TypeKindNumber}}}}
	in := testInput([]Model{contactModel()},
		Route{Method: "POST", Path: "/a", ResponseType: a},
		Route{Method: "POST", Path: "/b", ResponseType: b},
	)
	_, err := Generate(in)
	if err == nil || !strings.Contains(err.Error(), "two different Go types named Result") {
		t.Fatalf("Generate() error = %v, want a two-types-one-name error", err)
	}
	for _, want := range []string{"POST /a", "POST /b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}

	// The same type used twice, once through a pointer, is one type.
	nullableA := *a
	nullableA.Nullable = true
	in = testInput([]Model{contactModel()},
		Route{Method: "POST", Path: "/a", ResponseType: a},
		Route{Method: "POST", Path: "/b", ResponseType: &nullableA},
	)
	out := generateString(t, in)
	if strings.Count(out, "export interface Result") != 1 {
		t.Errorf("output should declare Result once:\n%s", out)
	}
}

func TestGenerate_CRUDOverrideAndIgnoredRoutes(t *testing.T) {
	in := testInput([]Model{contactModel("list", "get", "preview")},
		Route{Model: "contacts.contact", Name: "list", CRUDAction: "list", Scope: CollectionScope},
		Route{Model: "contacts.contact", Name: "preview", CRUDAction: "preview", Scope: CollectionScope},
		Route{Model: "contacts.contact", Name: "approve", CRUDAction: "workflow_transition", Scope: RecordScope},
		Route{Method: "GET", Path: "/live", Websocket: true},
		Route{Method: "GET", Path: "/events", Streaming: true},
	)
	out := generateString(t, in)
	for _, absent := range []string{"listContact:", "previewContact", "approveContact", "getLive", "getEvents"} {
		if strings.Contains(out, absent) {
			t.Errorf("output contains %s:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "listContacts: (params?: ListParams<Contact>)") {
		t.Errorf("output lacks the CRUD listContacts function the override serves:\n%s", out)
	}
}

func TestGenerate_ConnectorPrefixAndPathParams(t *testing.T) {
	in := testInput(nil, Route{Method: "PUT", Path: "/accounts/{account_id}/sync/{default}"})
	in.PathPrefix = "/connectors/contacts"
	out := generateString(t, in)
	want := "putAccountsSync: (accountId: string, default_: string, body?: unknown): Promise<unknown> =>\n" +
		"    apiClient.put<unknown>(`/connectors/contacts/accounts/${encodeURIComponent(accountId)}/sync/${encodeURIComponent(default_)}`, body),"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks\n%s\ngot:\n%s", want, out)
	}
}

func TestGenerate_OnlyUsedImports(t *testing.T) {
	out := generateString(t, testInput([]Model{contactModel()}))
	if strings.Contains(out, "import") {
		t.Errorf("a module with no ops or routes imports nothing, got:\n%s", out)
	}
	if !strings.Contains(out, "export const contactsApi = {\n};") || !strings.Contains(out, "contacts: typeof contactsApi;") {
		t.Errorf("output lacks the empty API object and its ModuleApis entry:\n%s", out)
	}
}

func TestValidate_ViewReferences(t *testing.T) {
	models := []Model{contactModel("get")}
	routes := []Route{{Model: "contacts.contact", Name: "merge", Scope: CollectionScope}}
	in := testInput(models, routes...)
	in.Views = []View{
		{Name: "contacts_list", Type: "list", Resource: "contacts.contact"},
		{Name: "contacts_form", Type: "form", Resource: "contacts.contact", Actions: []ViewAction{
			{Label: "New", Type: "create"},
			{Label: "Merge", Type: "route", Route: "contacts.merge"},
			{Label: "Frobnicate", Type: "route", Route: "contacts.frobnicate"},
			{Label: "Elsewhere", Type: "route", Route: "crm.markWon"},
		}},
		{Name: "custom_list", Type: "list", Resource: "contacts.contact", FetchRoute: "contacts.search"},
		{Name: "other_list", Type: "list", Resource: "crm.lead"},
	}

	err := Validate(in)
	if err == nil {
		t.Fatal("Validate() error = nil")
	}
	msg := err.Error()
	for _, want := range []string{
		`view "contacts_list": resource contacts.contact has no "list" op`,
		`view "contacts_form": resource contacts.contact has no "create" op, which its "create" action "New" needs`,
		`view "contacts_form" (resource contacts.contact): action "Frobnicate": route "contacts.frobnicate" names no action`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	for _, unwanted := range []string{"custom_list", "other_list", "crm.markWon", "Merge"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("error mentions %s, which is valid or out of scope:\n%s", unwanted, msg)
		}
	}
	if _, err := Generate(in); err == nil {
		t.Error("Generate() succeeded despite invalid views")
	}
}

func TestGenerate_ActionNameOnTwoModelsFails(t *testing.T) {
	company := Model{Name: "contacts.company", LabelPlural: "Companies"}
	for _, second := range []Route{
		{Model: "contacts.company", Name: "approve", Scope: RecordScope},
		{Model: "contacts.company", Name: "approve", CRUDAction: "workflow_transition", Scope: RecordScope},
	} {
		in := testInput([]Model{contactModel(), company},
			Route{Model: "contacts.contact", Name: "approve", Scope: RecordScope}, second)
		_, err := Generate(in)
		if err == nil || !strings.Contains(err.Error(), `share the action name "approve"`) ||
			!strings.Contains(err.Error(), "contacts.contact") || !strings.Contains(err.Error(), "contacts.company") {
			t.Errorf("Generate() error = %v, want both models named for the shared action name", err)
		}
	}

	// An engine.DefineAction overriding a workflow transition on its own model
	// is the same action, not a clash.
	in := testInput([]Model{contactModel()},
		Route{Model: "contacts.contact", Name: "approve", CRUDAction: "workflow_transition", Scope: RecordScope},
		Route{Model: "contacts.contact", Name: "approve", Scope: RecordScope},
	)
	if out := generateString(t, in); !strings.Contains(out, "approveContact: (id: string, body?: unknown)") {
		t.Errorf("output lacks the overriding approveContact:\n%s", out)
	}
}

func TestGenerate_PathParamNamedLikeGeneratedParam(t *testing.T) {
	out := generateString(t, testInput(nil,
		Route{Method: "POST", Path: "/imports/{body}"},
		Route{Method: "GET", Path: "/search/{params}/{options}"},
	))
	for _, want := range []string{
		"postImports: (bodyParam: string, body?: unknown)",
		"getSearch: (paramsParam: string, optionsParam: string, params?: Record<string, unknown>)",
		"export function useGetSearch(paramsParam: string, optionsParam: string, params?: Record<string, unknown>, options?:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	_, err := Generate(testInput(nil, Route{Method: "GET", Path: "/a/{a_b}/{aBParam}/{a-b}"}))
	if err == nil || !strings.Contains(err.Error(), `"aBParam"`) || !strings.Contains(err.Error(), "GET /a/{a_b}/{aBParam}/{a-b}") {
		t.Errorf("Generate() error = %v, want a clash on aBParam naming the route", err)
	}
}

func TestActionScope(t *testing.T) {
	for _, tt := range []struct{ name, declared, want string }{
		{"list", "", CollectionScope},
		{"create", RecordScope, CollectionScope},
		{"preview", "", CollectionScope},
		{"pivot", "", CollectionScope},
		{"get", CollectionScope, RecordScope},
		{"update", "", RecordScope},
		{"delete", "", RecordScope},
		{"merge", CollectionScope, CollectionScope},
		{"archive", "", RecordScope},
	} {
		if got := actionScope(tt.name, tt.declared); got != tt.want {
			t.Errorf("actionScope(%q, %q) = %q, want %q", tt.name, tt.declared, got, tt.want)
		}
	}
}
