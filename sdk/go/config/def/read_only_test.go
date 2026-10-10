package def

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/declare"
)

func checkReference[T any](t *testing.T, typ string, raw any, want T) {
	t.Helper()
	before := referenceDeclarations(t)
	_, suffix, _ := strings.CutLast(t.Name(), "/")
	key := "owner." + suffix
	r := Ref[T](key)
	newFakeHost(t, map[string]any{key: raw})

	if got := r.Get(); !reflect.DeepEqual(got, want) {
		t.Errorf("Get = %#v, want %#v", got, want)
	}
	if got, found := r.Lookup(); !found || !reflect.DeepEqual(got, want) {
		t.Errorf("Lookup = (%#v, %v), want (%#v, true)", got, found, want)
	}

	declarations := referenceDeclarations(t)
	if len(declarations) != len(before)+1 {
		t.Fatalf("recorded %d references, want one", len(declarations)-len(before))
	}
	if got := declarations[len(declarations)-1]; got != (RefDeclaration{Key: key, Type: typ}) {
		t.Errorf("declaration = %+v, want key=%s type=%s", got, key, typ)
	}
}

func referenceDeclarations(t *testing.T) []RefDeclaration {
	t.Helper()
	data, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	var declarations map[string][]RefDeclaration
	if err := json.Unmarshal(data, &declarations); err != nil {
		t.Fatal(err)
	}

	return declarations[KindRef]
}

func TestRef_TypesAndDecoding(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		checkReference(t, TypeString, "GH", "GH")
	})
	t.Run("boolean", func(t *testing.T) {
		checkReference(t, TypeBoolean, true, true)
	})
	t.Run("integer", func(t *testing.T) {
		checkReference(t, TypeInteger, int64(7), 7)
	})
	t.Run("float", func(t *testing.T) {
		checkReference(t, TypeFloat, int64(2), float64(2))
	})
	t.Run("duration", func(t *testing.T) {
		checkReference(t, TypeDuration, "15m", 15*time.Minute)
	})
	t.Run("strings", func(t *testing.T) {
		checkReference(t, TypeStringList, []any{"GH"}, []string{"GH"})
	})
	t.Run("integers", func(t *testing.T) {
		checkReference(t, TypeIntegerList, []any{int64(7)}, []int{7})
	})
	t.Run("floats", func(t *testing.T) {
		checkReference(t, TypeFloatList, []any{0.5}, []float64{0.5})
	})
	t.Run("json", func(t *testing.T) {
		type policy struct {
			Attempts int `json:"attempts"`
		}
		checkReference(t, TypeJSON, map[string]any{"attempts": int64(3)}, policy{Attempts: 3})
	})
	t.Run("json_null", func(t *testing.T) {
		checkReference[any](t, TypeJSON, nil, nil)
	})
	t.Run("named_string", func(t *testing.T) {
		type name string
		checkReference(t, TypeJSON, "name", name("name"))
	})
}

func TestReadOnly_UnsetFailuresAndResolvedZero(t *testing.T) {
	r := Ref[int]("owner.count")
	for _, tc := range []struct {
		name   string
		values map[string]any
		err    error
		found  bool
	}{
		{name: "unset"},
		{name: "host failure", err: errors.New("unavailable")},
		{name: "decode failure", values: map[string]any{"owner.count": "seven"}},
		{name: "resolved zero", values: map[string]any{"owner.count": int64(0)}, found: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost(t, tc.values)
			h.getErr = tc.err

			if got := r.Get(); got != 0 {
				t.Errorf("Get = %d, want zero", got)
			}
			if got, found := r.Lookup(); got != 0 || found != tc.found {
				t.Errorf("Lookup = (%d, %v), want (0, %v)", got, found, tc.found)
			}
		})
	}
}

func TestReadOnly_DeclarationErrorsPanic(t *testing.T) {
	r := Ref[string]("owner.key")
	installHost(t, nil)
	mustPanic(t, "Get without host", func() { r.Get() })
	mustPanic(t, "Lookup without host", func() { r.Lookup() })

	h := newFakeHost(t, nil)
	h.getErr = fmt.Errorf("read: %w", &abi.HostError{Code: abi.ErrCodeConfigKeyUndeclared})
	mustPanic(t, "Get undeclared", func() { r.Get() })
	mustPanic(t, "Lookup undeclared", func() { r.Lookup() })
}

func TestRef_InvalidNamesDoNotDeclare(t *testing.T) {
	before, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"", "key", ".key", "owner.", "owner.key.extra", "bad-owner.key", "owner.bad-key", "company.name"} {
		mustPanic(t, name, func() { Ref[string](name) })
	}

	after, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("invalid references changed the registry")
	}
}

func TestReadOnly_MethodsExposeOnlyReads(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[ReadOnly[string]](), reflect.TypeFor[*ReadOnly[string]]()} {
		if typ.NumMethod() != 2 {
			t.Errorf("%s has %d methods, want Get and Lookup", typ, typ.NumMethod())
		}
		for _, name := range []string{"Get", "Lookup"} {
			if _, ok := typ.MethodByName(name); !ok {
				t.Errorf("%s has no %s", typ, name)
			}
		}
	}
}

func TestCompanyHandles_ReadWithoutDeclarations(t *testing.T) {
	for _, tc := range []struct {
		key    string
		handle ReadOnly[string]
	}{
		{"company.name", CompanyName},
		{"company.address", CompanyAddress},
		{"company.tax_id", CompanyTaxID},
		{"company.logo_url", CompanyLogoURL},
	} {
		t.Run(tc.key, func(t *testing.T) {
			h := newFakeHost(t, map[string]any{tc.key: "profile value"})
			if got := tc.handle.Get(); got != "profile value" {
				t.Errorf("Get = %q", got)
			}

			h.values[tc.key] = "changed"
			if got, found := tc.handle.Lookup(); got != "changed" || !found {
				t.Errorf("Lookup after edit = (%q, %v)", got, found)
			}

			delete(h.values, tc.key)
			if got, found := tc.handle.Lookup(); got != "" || found {
				t.Errorf("unset Lookup = (%q, %v)", got, found)
			}
			if got := tc.handle.Get(); got != "" {
				t.Errorf("unset Get = %q", got)
			}
		})
	}

	for _, d := range referenceDeclarations(t) {
		if strings.HasPrefix(d.Key, "company.") {
			t.Errorf("platform handle recorded a reference: %+v", d)
		}
	}
}
