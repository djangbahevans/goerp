package def

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type fakeHost struct {
	values map[string]any
	getErr error
	setErr error
}

func (f *fakeHost) Get(key string) (any, bool, error) {
	v, ok := f.values[key]
	return v, ok, f.getErr
}

func (f *fakeHost) Set(key string, value any) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = value
	return nil
}

func installHost(t *testing.T, h Host) {
	t.Helper()
	prev := host
	SetHost(h)
	t.Cleanup(func() { host = prev })
}

func newFakeHost(t *testing.T, values map[string]any) *fakeHost {
	t.Helper()
	f := &fakeHost{values: values}
	installHost(t, f)
	return f
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	fn()
}

func TestValue_GetReturnsStoredValueOrDefault(t *testing.T) {
	country := String("default_country_code", "GH", Label("Country"))

	newFakeHost(t, map[string]any{})
	if got := country.Get(); got != "GH" {
		t.Errorf("unset Get = %q, want the default", got)
	}

	newFakeHost(t, map[string]any{"default_country_code": "NG"})
	if got := country.Get(); got != "NG" {
		t.Errorf("set Get = %q, want NG", got)
	}
}

func TestValue_LookupReportsWhetherSet(t *testing.T) {
	threshold := Float("dedup_threshold", 0.85, Label("Threshold"))

	newFakeHost(t, map[string]any{})
	if got, ok := threshold.Lookup(); ok || got != 0 {
		t.Errorf("unset Lookup = (%v, %v), want (0, false)", got, ok)
	}

	newFakeHost(t, map[string]any{"dedup_threshold": int64(1)})
	if got, ok := threshold.Lookup(); !ok || got != 1 {
		t.Errorf("set Lookup = (%v, %v), want (1, true)", got, ok)
	}
}

func TestValue_GetFallsBackToDefaultOnHostErrorOrMismatch(t *testing.T) {
	count := Int("batch_size", 50, Label("Batch size"))

	f := newFakeHost(t, map[string]any{"batch_size": 7})
	f.getErr = errors.New("unavailable")
	if got := count.Get(); got != 50 {
		t.Errorf("Get on host error = %d, want the default", got)
	}

	newFakeHost(t, map[string]any{"batch_size": "seven"})
	if got := count.Get(); got != 50 {
		t.Errorf("Get on a mismatched value = %d, want the default", got)
	}
}

func TestValue_GetAndLookupPanicOnUndeclaredKey(t *testing.T) {
	f := newFakeHost(t, map[string]any{})
	f.getErr = &abi.HostError{Code: abi.ErrCodeConfigKeyNotDeclared, Message: "not declared"}
	v := String("missing_from_manifest", "d", Label("K"))

	mustPanic(t, "Get", func() { v.Get() })
	mustPanic(t, "Lookup", func() { v.Lookup() })
}

func TestValue_RequiredKeyWithNoValueReturnsZero(t *testing.T) {
	apiKey := String("api_key", "", Label("API Key"), Required(), Encrypted())
	newFakeHost(t, map[string]any{})

	if got := apiKey.Get(); got != "" {
		t.Errorf("Get = %q, want the zero value", got)
	}
}

func TestValue_GetAndLookupPanicWithoutHost(t *testing.T) {
	installHost(t, nil)
	v := String("k", "d", Label("K"))

	mustPanic(t, "Get", func() { v.Get() })
	mustPanic(t, "Lookup", func() { v.Lookup() })
	if err := v.Set("x"); !errors.Is(err, ErrNoHost) {
		t.Errorf("Set err = %v, want ErrNoHost", err)
	}
}

func TestValue_SetWritesEncodedValue(t *testing.T) {
	f := newFakeHost(t, map[string]any{})
	every := Duration("reconcile_interval", 15*time.Minute, Label("Interval"))

	if err := every.Set(90 * time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := f.values["reconcile_interval"]; got != "1m30s" {
		t.Errorf("stored %v, want the duration string", got)
	}

	f.setErr = errors.New("config.key_not_declared")
	if err := every.Set(time.Minute); !errors.Is(err, f.setErr) {
		t.Errorf("Set err = %v, want the host's error", err)
	}
}

func TestDuration_RoundTripsGoDurationString(t *testing.T) {
	every := Duration("reconcile_interval", time.Hour, Label("Interval"))
	newFakeHost(t, map[string]any{})

	if err := every.Set(15 * time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := every.Get(); got != 15*time.Minute {
		t.Errorf("Get = %s, want 15m", got)
	}
}

func TestSliceKeys_RoundTrip(t *testing.T) {
	newFakeHost(t, map[string]any{})
	currencies := StringSlice("currencies", nil, Label("Currencies"))
	ids := IntSlice("ids", nil, Label("IDs"))
	weights := FloatSlice("weights", nil, Label("Weights"))

	if err := currencies.Set([]string{"GHS", "USD"}); err != nil {
		t.Fatal(err)
	}
	if err := ids.Set([]int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := weights.Set([]float64{0.5, 1.5}); err != nil {
		t.Fatal(err)
	}

	if got := currencies.Get(); !reflect.DeepEqual(got, []string{"GHS", "USD"}) {
		t.Errorf("currencies = %v", got)
	}
	if got := ids.Get(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("ids = %v", got)
	}
	if got := weights.Get(); !reflect.DeepEqual(got, []float64{0.5, 1.5}) {
		t.Errorf("weights = %v", got)
	}
}

func TestSliceKeys_EmptySliceEncodesAsArray(t *testing.T) {
	f := newFakeHost(t, map[string]any{})
	currencies := StringSlice("currencies", nil, Label("Currencies"))

	if err := currencies.Set(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.values["currencies"].([]any); !ok {
		t.Errorf("stored %T, want []any", f.values["currencies"])
	}
}

func TestSliceKeys_RejectMixedElementTypes(t *testing.T) {
	ids := IntSlice("ids", nil, Label("IDs"))
	newFakeHost(t, map[string]any{"ids": []any{int64(1), "two"}})

	if _, ok := ids.Lookup(); ok {
		t.Error("Lookup accepted an array with a non-numeric element")
	}
}

func TestJSON_DecodesStoredObjectIntoType(t *testing.T) {
	type retry struct {
		Max     int      `json:"max"`
		Backoff []string `json:"backoff"`
	}
	policy := JSON("retry_policy", retry{Max: 3}, Label("Retry policy"))
	newFakeHost(t, map[string]any{"retry_policy": map[string]any{"max": int64(5), "backoff": []any{"1s", "5s"}}})

	want := retry{Max: 5, Backoff: []string{"1s", "5s"}}
	if got := policy.Get(); !reflect.DeepEqual(got, want) {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestJSON_SetStoresGenericValue(t *testing.T) {
	type retry struct {
		Max int `json:"max"`
	}
	f := newFakeHost(t, map[string]any{})
	policy := JSON("retry_policy", retry{}, Label("Retry policy"))

	if err := policy.Set(retry{Max: 4}); err != nil {
		t.Fatal(err)
	}
	if got := policy.Get(); got.Max != 4 {
		t.Errorf("Get = %+v, want Max 4", got)
	}
	if _, ok := f.values["retry_policy"].(map[string]any); !ok {
		t.Errorf("stored %T, want map[string]any", f.values["retry_policy"])
	}
}

func TestDefinition_ExposesKeyTypeDefaultAndSpec(t *testing.T) {
	v := Float("dedup_threshold", 0.85,
		Label("Threshold"), Description("Match cutoff"), Category("Defaults"),
		Min(0), Max(1), Public(), RestartRequired(),
		Choices(Choice{Value: "a", Label: "A"}))

	var d Definition = v
	if d.Key() != "dedup_threshold" || d.Type() != TypeFloat || d.Default() != 0.85 {
		t.Errorf("Key/Type/Default = %q/%q/%v", d.Key(), d.Type(), d.Default())
	}
	spec := d.Spec()
	if spec.Label != "Threshold" || spec.Description != "Match cutoff" || spec.Category != "Defaults" {
		t.Errorf("spec text = %+v", spec)
	}
	if spec.Min == nil || *spec.Min != 0 || spec.Max == nil || *spec.Max != 1 {
		t.Errorf("spec bounds = %v..%v", spec.Min, spec.Max)
	}
	if !spec.Public || !spec.RestartRequired || spec.Encrypted || spec.Generated || spec.Required {
		t.Errorf("spec flags = %+v", spec)
	}
	if len(spec.Choices) != 1 {
		t.Errorf("spec choices = %v", spec.Choices)
	}
}

func TestConstructors_ReportManifestTypes(t *testing.T) {
	opt := Label("L")
	tests := []struct {
		def  Definition
		want string
	}{
		{String("a", "", opt), TypeString},
		{Bool("a", false, opt), TypeBoolean},
		{Int("a", 0, opt), TypeInteger},
		{Float("a", 0, opt), TypeFloat},
		{Duration("a", 0, opt), TypeDuration},
		{StringSlice("a", nil, opt), TypeStringList},
		{IntSlice("a", nil, opt), TypeIntegerList},
		{FloatSlice("a", nil, opt), TypeFloatList},
		{JSON("a", struct{}{}, opt), TypeJSON},
	}
	for _, tt := range tests {
		if got := tt.def.Type(); got != tt.want {
			t.Errorf("Type() = %q, want %q", got, tt.want)
		}
	}
}

func TestDefine_PanicsOnInvalidDefinition(t *testing.T) {
	mustPanic(t, "empty key", func() { String("", "", Label("L")) })
	mustPanic(t, "dotted key", func() { String("contacts.key", "", Label("L")) })
	mustPanic(t, "missing label", func() { String("k", "") })
	mustPanic(t, "required non-zero default", func() { String("k", "x", Label("L"), Required()) })
	mustPanic(t, "required non-empty slice", func() { StringSlice("k", []string{"x"}, Label("L"), Required()) })
	mustPanic(t, "bad pattern", func() { String("k", "", Label("L"), Pattern("(")) })
}

func TestDefine_RequiredAcceptsZeroDefaults(t *testing.T) {
	String("k", "", Label("L"), Required())
	Int("k", 0, Label("L"), Required())
	StringSlice("k", nil, Label("L"), Required())
	StringSlice("k", []string{}, Label("L"), Required())
}

func TestPackageLinksNoHostCallLayer(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for dep := range strings.Lines(string(out)) {
		dep = strings.TrimSpace(dep)
		if strings.HasSuffix(dep, "/sdk/go/db") || strings.Contains(dep, "/sdk/go/internal/") {
			t.Errorf("config/def depends on %s", dep)
		}
	}
}
