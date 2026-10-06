package module

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"testing"
)

type fakeCollector struct {
	key   string
	kinds []string
	value any
	err   error
}

func (c fakeCollector) Key() string { return c.key }

func (c fakeCollector) Kinds() []string { return c.kinds }

func (c fakeCollector) Collect(Declarations) (any, error) { return c.value, c.err }

func withCollectors(t *testing.T, cs ...Collector) {
	t.Helper()
	orig := collectors
	collectors = cs
	t.Cleanup(func() { collectors = orig })
}

func TestRegisterCollector_DuplicateKeyPanics(t *testing.T) {
	withCollectors(t)
	registerCollector(fakeCollector{key: "emits"})

	defer func() {
		if recover() == nil {
			t.Error("a second collector for one key did not panic")
		}
	}()
	registerCollector(fakeCollector{key: "emits"})
}

func TestCollectBlocks_OrdersByKeyAndLeavesUndeclaredNil(t *testing.T) {
	blocks, err := collectBlocks(nil, []Collector{
		fakeCollector{key: "subscribes", value: []string{"a"}},
		fakeCollector{key: "emits"},
		fakeCollector{key: "job_types", value: map[string]int{"b": 2, "a": 1}},
		fakeCollector{key: "typed_nil", value: []string(nil)},
		fakeCollector{key: "empty_slice", value: []string{}},
		fakeCollector{key: "empty_map", value: map[string]int{}},
	})
	if err != nil {
		t.Fatalf("collectBlocks: %v", err)
	}

	var keys []string
	values := map[string]string{}
	for _, b := range blocks {
		keys = append(keys, b.key)
		values[b.key] = string(b.value)
	}
	wantKeys := []string{"emits", "empty_map", "empty_slice", "job_types", "subscribes", "typed_nil"}
	if !slices.Equal(keys, wantKeys) {
		t.Fatalf("keys = %v, want %v", keys, wantKeys)
	}
	for _, key := range []string{"emits", "empty_map", "empty_slice", "typed_nil"} {
		if values[key] != "" {
			t.Errorf("%s = %s, want the key left out", key, values[key])
		}
	}
	if values["job_types"] != `{"a":1,"b":2}` {
		t.Errorf("job_types = %s, want a deterministic encoding", values["job_types"])
	}
}

func TestCollectBlocks_CollectorErrorNamesTheKey(t *testing.T) {
	boom := errors.New("job \"x\" has no handler")

	_, err := collectBlocks(nil, []Collector{fakeCollector{key: "job_types", err: boom}})
	if !errors.Is(err, boom) || err.Error() != `collect job_types: job "x" has no handler` {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeDeclarations(t *testing.T) {
	type widget struct {
		Name string `json:"name"`
	}
	d := Declarations{"widget": {jsontext.Value(`{"name":"a"}`), jsontext.Value(`{"name":"b"}`)}}

	got, err := decodeDeclarations[widget](d, "widget")
	if err != nil || !slices.Equal(got, []widget{{"a"}, {"b"}}) {
		t.Fatalf("decodeDeclarations = %v, %v", got, err)
	}
	if got, err := decodeDeclarations[widget](d, "other"); err != nil || len(got) != 0 {
		t.Errorf("an absent kind = %v, %v; want none", got, err)
	}

	d["bad"] = []jsontext.Value{jsontext.Value(`[1]`)}
	if _, err := decodeDeclarations[widget](d, "bad"); err == nil {
		t.Error("a declaration that does not decode was accepted")
	}
}

func TestCollectBlocks_DeclaredKindWithNoCollectorFails(t *testing.T) {
	decls := Declarations{"event": {jsontext.Value(`{}`)}, "evnt": {jsontext.Value(`{}`)}}

	_, err := collectBlocks(decls, []Collector{fakeCollector{key: "emits", kinds: []string{"event"}}})
	if err == nil || err.Error() != "declarations of kind evnt have no collector" {
		t.Errorf("err = %v", err)
	}
}
