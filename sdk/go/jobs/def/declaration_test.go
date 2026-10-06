package def

import (
	"encoding/json/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

type innerPayload struct {
	Inner string `msgpack:"inner_key"`
}

type renamedEmbed struct {
	innerPayload `msgpack:"base"`
	Own          string `msgpack:"own"`
}

type noinlineEmbed struct {
	innerPayload `msgpack:"base,noinline"`
}

type shadowedEmbed struct {
	Inner string `msgpack:"inner_key"`
	innerPayload
}

type timeEmbed struct {
	time.Time
	Name string
}

type namedStringEmbed struct {
	ID
}

type ID string

type fieldsPayload struct {
	innerPayload
	FileID  string `msgpack:"file_id,omitempty"`
	Plain   int
	Skipped string `msgpack:"-"`
	_       string
	Ptr     *innerPayload `msgpack:"ptr"`
}

func TestPayloadFields(t *testing.T) {
	tests := []struct {
		name       string
		typ        reflect.Type
		wantStruct bool
		want       []string
	}{
		{"tags, inlined embed, skipped and unexported fields", reflect.TypeFor[fieldsPayload](), true, []string{"inner_key", "file_id", "Plain", "ptr"}},
		{"a tagged embedded struct is still inlined", reflect.TypeFor[renamedEmbed](), true, []string{"inner_key", "own"}},
		{"noinline keeps the embedded struct as a field", reflect.TypeFor[noinlineEmbed](), true, []string{"base"}},
		{"an embedded struct that would shadow a key stays a field", reflect.TypeFor[shadowedEmbed](), true, []string{"inner_key", "innerPayload"}},
		{"an embedded struct that encodes itself stays a field", reflect.TypeFor[timeEmbed](), true, []string{"Time", "Name"}},
		{"an embedded non-struct is a field named for its type", reflect.TypeFor[namedStringEmbed](), true, []string{"ID"}},
		{"pointer to struct", reflect.TypeFor[*innerPayload](), true, []string{"inner_key"}},
		{"empty struct", reflect.TypeFor[struct{}](), true, nil},
		{"map payload", reflect.TypeFor[map[string]any](), false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStruct, got := payloadFields(tt.typ)
			if gotStruct != tt.wantStruct || !slices.Equal(got, tt.want) {
				t.Errorf("payloadFields = %v, %v; want %v, %v", gotStruct, got, tt.wantStruct, tt.want)
			}
		})
	}
}

func declarationsOf[T any](t *testing.T, kind, name string, nameOf func(T) string) T {
	t.Helper()
	data, err := declare.Export()
	if err != nil {
		t.Fatalf("declare.Export: %v", err)
	}
	var byKind map[string][]T
	if err := json.Unmarshal(data, &byKind); err != nil {
		t.Fatalf("decode declarations: %v", err)
	}
	for _, d := range slices.Backward(byKind[kind]) {
		if nameOf(d) == name {
			return d
		}
	}
	t.Fatalf("no %s declaration named %q", kind, name)
	panic("unreachable")
}

func TestDefine_RecordsTheJobDeclaration(t *testing.T) {
	Define[fieldsPayload]("declared_import", Label("Import"), Description("Imports"), Queue(QueueBulk),
		Timeout(90*time.Second), MaxAttempts(4), Priority(70), UniqueBy("file_id"))

	got := declarationsOf(t, KindJob, "declared_import", func(d JobDeclaration) string { return d.Name })
	want := JobDeclaration{
		Name: "declared_import", Label: "Import", Description: "Imports", Queue: QueueBulk,
		TimeoutSeconds: 90, MaxAttempts: 4, Priority: 70, UniqueBy: "file_id",
		PayloadStruct: true, PayloadFields: []string{"inner_key", "file_id", "Plain", "ptr"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("declaration = %+v, want %+v", got, want)
	}
}

func TestDefineCron_RecordsTheCronDeclarationWithDefaults(t *testing.T) {
	DefineCron("declared_sweep", Schedule("0 3 * * *"), Label("Sweep"), DisabledByDefault())

	got := declarationsOf(t, KindCron, "declared_sweep", func(d CronDeclaration) string { return d.Name })
	want := CronDeclaration{
		Name: "declared_sweep", Label: "Sweep", Schedule: "0 3 * * *", DisabledByDefault: true,
		TimeoutSeconds: 3600, Queue: QueueBulk,
	}
	if got != want {
		t.Errorf("declaration = %+v, want %+v", got, want)
	}
}

func TestDefine_TimeoutMustBeWholeSeconds(t *testing.T) {
	mustPanic(t, "fractional job timeout", func() { Define[struct{}]("fraction_job", Label("X"), Timeout(1500*time.Millisecond)) })
	mustPanic(t, "fractional cron timeout", func() {
		DefineCron("fraction_cron", Schedule("* * * * *"), Label("X"), Timeout(time.Second+time.Nanosecond))
	})
}

func TestDefineProvider_RecordsNoJobDeclaration(t *testing.T) {
	data, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	var before map[string][]JobDeclaration
	if err := json.Unmarshal(data, &before); err != nil {
		t.Fatal(err)
	}

	DefineProvider[struct{}, struct{}]("sms_provider", "declared_sms_send")

	data, err = declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	var after map[string][]JobDeclaration
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if len(after[KindJob]) != len(before[KindJob]) {
		t.Errorf("DefineProvider recorded a job declaration: %d, was %d", len(after[KindJob]), len(before[KindJob]))
	}
}
