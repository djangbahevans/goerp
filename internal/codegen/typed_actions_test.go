package codegen

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadLocal_TypedActionDefinitions(t *testing.T) {
	dir := copyExampleModule(t)
	frontend := filepath.Join(dir, "frontend", "src", "Actions.tsx")
	writeFile(t, frontend, strings.Join([]string{
		`useAction('contacts.merge');`,
		`useAction('contacts.archive');`,
		`useAction('contacts.reindex');`,
		`useAction('contacts.create');`,
	}, "\n"))

	in, err := LoadLocal(t.Context(), dir)
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}

	in.ActionRefs, err = ScanActionRefs(dir, filepath.Join(dir, "frontend", "src", "api", "generated.ts"))
	if err != nil {
		t.Fatalf("ScanActionRefs: %v", err)
	}

	out := generateString(t, in)
	for _, want := range []string{
		"export interface MergeContactsRequest {\n  target_id: string;\n  source_ids: string[];\n  note?: string | null;\n}",
		"mergeContact: (body: MergeContactsRequest): Promise<Contact>",
		"archiveContact: (id: string): Promise<unknown>",
		"callAction<unknown, { id: string }>('contacts.archive', { id })",
		"reindexContact: (): Promise<unknown>",
		"callAction<unknown, void>('contacts.reindex')",
		"useAction<unknown, void>('contacts.reindex', options)",
		"createCompany: (body: Record<string, unknown>)",
		"postImport: (body: ImportRequest): Promise<ImportResult>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	for _, view := range in.Views {
		for actionIndex, action := range view.Actions {
			if action.Type != "route" {
				continue
			}

			t.Run(view.Name+"/"+action.Label, func(t *testing.T) {
				broken := *in
				broken.Views = []View{view}
				broken.Views[0].Actions = slices.Clone(view.Actions)
				broken.Views[0].Actions[actionIndex].Route = "contacts.undefined"

				_, err := Generate(&broken)
				if err == nil {
					t.Fatal("Generate succeeded for an undefined view action")
				}

				for _, want := range []string{view.Name, action.Label, `module contacts declares no action named "undefined"`} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
				}
			})
		}
	}

	t.Run("useAction", func(t *testing.T) {
		writeFile(t, frontend, `useAction('contacts.undefined');`)

		refs, err := ScanActionRefs(dir, "")
		if err != nil {
			t.Fatalf("ScanActionRefs: %v", err)
		}

		broken := *in
		broken.ActionRefs = refs

		_, err = Generate(&broken)
		if err == nil || !strings.Contains(err.Error(), frontend+`:1: route "contacts.undefined" names no action`) {
			t.Fatalf("Generate error = %v, want the undefined action's source location", err)
		}
	})
}
