package notify

import (
	"encoding/json/v2"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/vmihailenco/msgpack/v5"
)

type orderConfirmedData struct {
	OrderID    string
	CustomerID string `json:"customer_id"`
	Total      int
	Paid       bool
	Skipped    string `json:"-"`
	hidden     string
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: want a panic", name)
		}
	}()
	fn()
}

func TestDefine_RejectsInvalidDefinitions(t *testing.T) {
	label := Label("L")
	mustPanic(t, "name with a module prefix", func() { Define[struct{}]("sales.order_confirmed", label) })
	mustPanic(t, "name not snake_case", func() { Define[struct{}]("OrderConfirmed", label) })
	mustPanic(t, "empty name", func() { Define[struct{}]("", label) })
	mustPanic(t, "missing label", func() { Define[struct{}]("no_label") })
	mustPanic(t, "unknown priority", func() { Define[struct{}]("bad_priority", label, DefaultPriority("urgent")) })
	mustPanic(t, "unknown channel", func() { Define[struct{}]("bad_channel", label, AvailableChannels(ChannelInApp, "pager")) })
	mustPanic(t, "default without in_app", func() {
		Define[struct{}]("no_default_in_app", label, DefaultChannels(ChannelEmail), AvailableChannels(ChannelInApp, ChannelEmail))
	})
	mustPanic(t, "available without in_app", func() { Define[struct{}]("no_available_in_app", label, AvailableChannels(ChannelEmail)) })
	mustPanic(t, "default outside available", func() { Define[struct{}]("default_not_available", label, DefaultChannels(ChannelInApp, ChannelEmail)) })
	mustPanic(t, "template for an unknown channel", func() { Define[struct{}]("bad_template_channel", label, Template("pager", "p.txt")) })
	mustPanic(t, "empty template path", func() { Define[struct{}]("empty_template", label, Template(ChannelEmail, "")) })
}

func TestDefine_RejectsADuplicateName(t *testing.T) {
	Define[struct{}]("duplicate_name", Label("First"))

	mustPanic(t, "second definition with the same name", func() { Define[orderConfirmedData]("duplicate_name", Label("Second")) })
}

func TestDefine_AcceptsAFullDefinition(t *testing.T) {
	d := Define[orderConfirmedData]("full_definition",
		Label("Order confirmed"), Description("Sent when an order is confirmed"),
		DefaultChannels(ChannelInApp, ChannelEmail),
		AvailableChannels(ChannelInApp, ChannelEmail, ChannelSMS, ChannelPush),
		DefaultPriority(High),
		Template(ChannelEmail, "mail/{locale}.html"),
	)

	if d.Name() != "full_definition" {
		t.Errorf("Name() = %q, want full_definition", d.Name())
	}
}

func TestSendInput_CarriesTheNameTheDataAndTheOptions(t *testing.T) {
	d := Define[orderConfirmedData]("send_input", Label("L"))

	in, err := d.sendInput("user-1", orderConfirmedData{OrderID: "o-1", CustomerID: "c-1", Total: 1_000_000, Paid: true, Skipped: "x", hidden: "y"},
		[]NotifyOption{HighPriority(), WithIdempotencyKey("k")})
	if err != nil {
		t.Fatalf("sendInput: %v", err)
	}

	if in.UserID != "user-1" || in.Type != "send_input" || in.TemplateKey != "send_input" {
		t.Errorf("input = %+v, want user-1 sent as send_input with that template key", in)
	}
	if in.Opts.Priority != "high" || in.Opts.IdempotencyKey != "k" {
		t.Errorf("options = %+v, want high priority and key k", in.Opts)
	}
	var data map[string]any
	if err := msgpack.Unmarshal(in.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	want := map[string]any{"OrderID": "o-1", "customer_id": "c-1", "Total": int64(1_000_000), "Paid": true}
	if len(data) != len(want) {
		t.Fatalf("data = %v, want %v", data, want)
	}
	for k, v := range want {
		if got := data[k]; fmt.Sprint(got) != fmt.Sprint(v) {
			t.Errorf("data[%q] = %#v, want %#v", k, got, v)
		}
	}
}

func TestSendInput_ReportsDataThatIsNotAnObject(t *testing.T) {
	d := Define[[]string]("not_an_object", Label("L"))

	if _, err := d.sendInput("user-1", []string{"a"}, nil); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Errorf("sendInput error = %v, want a message that data must be a JSON object", err)
	}
}

func TestSendInput_RejectsAZeroDef(t *testing.T) {
	var zero Def[struct{}]

	if _, err := zero.sendInput("user-1", struct{}{}, nil); err == nil {
		t.Error("sendInput on a zero Def: want an error, not a send with an empty type")
	}
}

func TestEncodeData_KeepsIntegersAndNesting(t *testing.T) {
	encoded, err := encodeData(map[string]any{"count": 3, "ratio": 1.5, "tags": []string{"a", "b"}, "inner": map[string]any{"ok": true, "none": nil}})
	if err != nil {
		t.Fatalf("encodeData: %v", err)
	}
	var got map[string]any
	if err := msgpack.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	inner, _ := got["inner"].(map[string]any)
	tags, _ := got["tags"].([]any)
	if fmt.Sprint(got["count"]) != "3" || got["ratio"] != 1.5 || len(tags) != 2 || inner["ok"] != true {
		t.Errorf("decoded = %#v", got)
	}
	if v, present := inner["none"]; !present || v != nil {
		t.Errorf("inner[none] = %#v, present %v, want a nil value", v, present)
	}
}

func TestDeclaration_IsRecordedForManifestGeneration(t *testing.T) {
	Define[orderConfirmedData]("declared_type",
		Label("Declared"), Description("d"), DefaultChannels(ChannelInApp, ChannelSMS),
		AvailableChannels(ChannelInApp, ChannelSMS), DefaultPriority(High), Template(ChannelSMS, "sms/{locale}.txt"))
	Define[map[string]any]("declared_map", Label("Map data"))

	raw, err := declare.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var byKind map[string][]NotificationDeclaration
	if err := json.Unmarshal(raw, &byKind); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	find := func(name string) NotificationDeclaration {
		t.Helper()
		for _, d := range byKind[KindNotification] {
			if d.Name == name {
				return d
			}
		}
		t.Fatalf("no %q declaration in %v", name, byKind[KindNotification])
		return NotificationDeclaration{}
	}

	typed := find("declared_type")
	if typed.Label != "Declared" || typed.Description != "d" || typed.DefaultPriority != High ||
		!slices.Equal(typed.DefaultChannels, []string{ChannelInApp, ChannelSMS}) ||
		typed.Templates[ChannelSMS] != "sms/{locale}.txt" || !typed.DataStruct {
		t.Errorf("declaration = %+v", typed)
	}
	wantSchema := map[string]string{"OrderID": "string", "customer_id": "string", "Total": "int", "Paid": "bool"}
	if len(typed.DataSchema) != len(wantSchema) {
		t.Fatalf("data schema = %v, want %v", typed.DataSchema, wantSchema)
	}
	for k, v := range wantSchema {
		if typed.DataSchema[k] != v {
			t.Errorf("data schema[%q] = %q, want %q", k, typed.DataSchema[k], v)
		}
	}
	if m := find("declared_map"); m.DataStruct || m.DataSchema != nil {
		t.Errorf("map data declaration = %+v, want no struct schema", m)
	}
	if def := find("declared_map"); !slices.Equal(def.DefaultChannels, []string{ChannelInApp}) || def.DefaultPriority != Normal {
		t.Errorf("defaults = %+v, want in_app only at normal priority", def)
	}
}

func TestSend_DataOfTheWrongTypeDoesNotCompile(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "build", "-o", t.TempDir()+"/wrongdata", "./testdata/wrongdata").CombinedOutput()

	if err == nil {
		t.Fatal("go build succeeded, want a compile error for data of the wrong type")
	}
	if !strings.Contains(string(out), "cannot use") {
		t.Errorf("build output = %s, want a type mismatch error", out)
	}
}
