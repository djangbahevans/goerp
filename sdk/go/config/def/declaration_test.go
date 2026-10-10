package def

import (
	"encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

func configDeclarations(t *testing.T) []Declaration {
	t.Helper()

	data, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	var registry map[string][]Declaration
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatal(err)
	}

	return registry[KindConfig]
}

func TestDefinitions_RegisterEncodedDefaultsAndMetadata(t *testing.T) {
	before := len(configDeclarations(t))
	Duration("registered_interval", 15*time.Minute, Label("Interval"), MinDuration(time.Minute))
	String("registered_secret", "", Label("Secret"), Required(), Encrypted())
	defaults := []int{1, 2}
	IntSlice("registered_counts", defaults, Label("Counts"))
	defaults[0] = 9

	entries := configDeclarations(t)[before:]
	if len(entries) != 3 {
		t.Fatalf("registered %d definitions, want 3", len(entries))
	}
	if interval := entries[0]; interval.Key != "registered_interval" || interval.Type != TypeDuration || interval.Default != "15m0s" || interval.Label != "Interval" || interval.Min != "1m0s" {
		t.Errorf("interval declaration = %+v", interval)
	}
	if secret := entries[1]; secret.Default != nil || !secret.Required || !secret.Encrypted {
		t.Errorf("required declaration = %+v", secret)
	}
	if counts := entries[2]; counts.Type != TypeIntegerList || !reflect.DeepEqual(counts.Default, []any{float64(1), float64(2)}) {
		t.Errorf("slice default snapshot = %+v", counts)
	}
}

func TestInvalidDefinitions_DoNotRegister(t *testing.T) {
	before, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}

	mustPanic(t, "invalid key", func() { String("bad.key", "", Label("Key")) })
	mustPanic(t, "nonzero required default", func() { Int("required_count", 1, Label("Count"), Required()) })
	mustPanic(t, "unencodable JSON default", func() { JSON("channel", make(chan int), Label("Channel")) })

	after, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("invalid definitions changed the registry")
	}
}
