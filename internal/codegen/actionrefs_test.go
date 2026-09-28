package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanActionRefs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "frontend", "src")
	writeFile(t, filepath.Join(src, "Orders.tsx"), strings.Join([]string{
		`const confirm = useAction('sales.confirm');`,
		`const merge = useAction<Contact, { ids: Array<string> }>("contacts.merge", { onSuccess });`,
		"await callAction(`sales.cancel`, { id });",
		"const dynamic = useAction(`sales.${name}`);",
		`const computed = useAction(routeName);`,
	}, "\n"))
	writeFile(t, filepath.Join(src, "notes.md"), `useAction('sales.ignored')`)
	writeFile(t, filepath.Join(src, "node_modules", "dep", "index.ts"), `useAction('dep.ignored')`)
	generated := filepath.Join(src, "api", "generated.ts")
	writeFile(t, generated, `useAction('sales.removed')`)

	refs, err := ScanActionRefs(dir, generated)
	if err != nil {
		t.Fatalf("ScanActionRefs() error: %v", err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, fmt.Sprintf("%s:%d %s", filepath.Base(r.File), r.Line, r.Route))
	}
	want := []string{"Orders.tsx:1 sales.confirm", "Orders.tsx:2 contacts.merge", "Orders.tsx:3 sales.cancel"}
	if !slices.Equal(got, want) {
		t.Errorf("ScanActionRefs() = %v, want %v", got, want)
	}
}

func TestScanActionRefs_NoFrontend(t *testing.T) {
	dir := t.TempDir()
	refs, err := ScanActionRefs(dir, filepath.Join(dir, "frontend", "src", "api", "generated.ts"))
	if err != nil || refs != nil {
		t.Errorf("ScanActionRefs() = %v, %v, want nil, nil", refs, err)
	}
}

func TestValidate_ActionRefs(t *testing.T) {
	in := testInput([]Model{contactModel("list")}, Route{Method: "POST", Model: "contacts.contact", Name: "merge", Scope: CollectionScope})
	in.ActionRefs = []ActionRef{
		{File: "src/A.tsx", Line: 3, Route: "contacts.merge"},
		{File: "src/A.tsx", Line: 7, Route: "contacts.mrege"},
		{File: "src/B.tsx", Line: 1, Route: "merge"},
		{File: "src/B.tsx", Line: 2, Route: "sales.confirm"},
	}

	err := Validate(in)
	if err == nil {
		t.Fatal("Validate() = nil, want errors for the unknown and malformed action references")
	}
	msg := err.Error()
	for _, want := range []string{
		`src/A.tsx:7: route "contacts.mrege" names no action: module contacts declares no action named "mrege"`,
		`src/B.tsx:1: route "merge" is not "module.actionName"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Validate() error = %q, want it to contain %q", msg, want)
		}
	}
	for _, unwanted := range []string{"src/A.tsx:3", "sales.confirm"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("Validate() error = %q, want no error for %s", msg, unwanted)
		}
	}
}
