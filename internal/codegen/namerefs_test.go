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

func TestScanNameRefs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "frontend", "src")
	writeFile(t, filepath.Join(src, "Orders.tsx"), strings.Join([]string{
		`permissionRef('sales:order:read');`,
		`resourceRef<Contact & Record<string, Array<{ id: string }>>>("contacts.contact");`,
		"actionRef<Order, { id: string }>(`sales.confirm`);",
		`const resource = resourceRef<`,
		`  { callback: () => Array<string> }`,
		`>(`,
		`  /* a foreign resource */ 'hr.employee',`,
		`);`,
		"actionRef(`sales.${name}`);",
		`actionRef(routeName);`,
		`resourceRef('hr.' + model);`,
		`permissionRef('hr:employee:read'.toUpperCase());`,
		`useAction('sales.unknown'); callAction('sales.unknown');`,
		`// actionRef('sales.comment');`,
		`/* resourceRef('sales.comment');`,
		`permissionRef('sales:comment'); */`,
		`const text = "actionRef('sales.text')";`,
		`const more = 'resourceRef("sales.text")';`,
		`notactionRef('sales.suffix'); actionRefOther('sales.prefix');`,
		`actionRef('sales.\x63ancel');`,
		`permissionRef("hr:employee:re\u0061d");`,
		`permissionRef('hr:${literal}');`,
		"const nested = `permissionRef('ignored:text') ${actionRef('sales.nested')}`;",
		"const deeper = `text ${`nested ${resourceRef('hr.employee')}`}`;",
		`actionRef('sales.\u{63}ancel');`,
		`actionRef('sales.\cancel');`,
		"permissionRef(`hr:\\${literal}`);",
	}, "\n"))
	writeFile(t, filepath.Join(src, "nested", "other.ts"), `resourceRef('hr.employee')`)
	writeFile(t, filepath.Join(src, "notes.md"), `actionRef('sales.ignored')`)
	writeFile(t, filepath.Join(src, "node_modules", "dep", "index.ts"), `actionRef('dep.ignored')`)
	writeFile(t, filepath.Join(src, ".cache", "index.ts"), `actionRef('dep.ignored')`)
	generated := filepath.Join(src, "api", "generated.ts")
	writeFile(t, generated, `actionRef('sales.removed')`)

	refs, err := ScanNameRefs(dir, generated)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, ref := range refs {
		got = append(got, fmt.Sprintf("%s:%d %s %s", filepath.Base(ref.File), ref.Line, ref.Helper, ref.Name))
	}
	want := []string{
		"Orders.tsx:1 permissionRef sales:order:read",
		"Orders.tsx:2 resourceRef contacts.contact",
		"Orders.tsx:3 actionRef sales.confirm",
		"Orders.tsx:4 resourceRef hr.employee",
		"Orders.tsx:20 actionRef sales.cancel",
		"Orders.tsx:21 permissionRef hr:employee:read",
		"Orders.tsx:22 permissionRef hr:${literal}",
		"Orders.tsx:23 actionRef sales.nested",
		"Orders.tsx:24 resourceRef hr.employee",
		"Orders.tsx:25 actionRef sales.cancel",
		"Orders.tsx:26 actionRef sales.cancel",
		"Orders.tsx:27 permissionRef hr:${literal}",
		"other.ts:1 resourceRef hr.employee",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ScanNameRefs() = %v, want %v", got, want)
	}
}

func TestScanNameRefs_NoFrontend(t *testing.T) {
	dir := t.TempDir()
	refs, err := ScanNameRefs(dir, "")
	if err != nil || refs != nil {
		t.Errorf("ScanNameRefs() = %v, %v, want nil, nil", refs, err)
	}
}

func TestValidate_NameRefs(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			in := testInput([]Model{contactModel()}, Route{Name: "merge"})
			in.Catalog["contacts"].Permissions["contacts:contact:read"] = true
			in.Catalog["hr"] = catalogFor([]Model{{Name: "hr.employee"}}, []Route{{Name: "approve"}})
			in.Catalog["hr"].Permissions["hr:employee:read"] = true
			in.CompleteCatalog = complete

			for _, tc := range []struct {
				helper  string
				own     string
				foreign string
				unknown string
			}{
				{"resourceRef", "contacts.contact", "hr.employee", "hr.employe"},
				{"actionRef", "contacts.merge", "hr.approve", "hr.aproove"},
				{"permissionRef", "contacts:contact:read", "hr:employee:read", "hr:employee:reed"},
			} {
				for _, name := range []string{tc.own, tc.foreign, tc.unknown, "unloaded.missing", "unqualified", ""} {
					in.NameRefs = []NameRef{{File: "src/A.tsx", Line: 7, Helper: tc.helper, Name: name}}
					err := Validate(in)
					wantErr := name == tc.own || (complete && name != tc.foreign)
					if (err != nil) != wantErr {
						t.Errorf("%s(%q): error = %v, want error %t", tc.helper, name, err, wantErr)
					}
					if err != nil {
						for _, want := range []string{"src/A.tsx:7", tc.helper, fmt.Sprintf("%q", name)} {
							if !strings.Contains(err.Error(), want) {
								t.Errorf("error %q lacks %q", err, want)
							}
						}
					}
				}
			}
		})
	}
}
