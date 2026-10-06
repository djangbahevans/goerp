package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// eventsFixtureMain emits one event of its own and subscribes to two versions
// of another module's event, defining that event itself with the payload
// fields it reads, the way a bridge module does.
const eventsFixtureMain = `package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
)

type widgetCreatedPayload struct {
	WidgetID string  ` + "`msgpack:\"widget_id\"`" + `
	Note     *string ` + "`msgpack:\"note\"`" + `
}

type orderPayload struct {
	OrderID string ` + "`msgpack:\"order_id\"`" + `
}

var (
	widgetCreated    = events.Define[widgetCreatedPayload]("widgets.widget.created", events.Description("A widget was created"))
	orderConfirmedV1 = events.Define[orderPayload]("sales.order.confirmed")
	orderConfirmedV2 = events.Define[orderPayload]("sales.order.confirmed", events.Version(2))
)

func handleConfirmedV1(events.Event[orderPayload]) error { return nil }

func init() {
	_ = widgetCreated
	engine.Subscribe(orderConfirmedV1, handleConfirmedV1, engine.Sync())
	engine.Subscribe(orderConfirmedV2, func(events.Event[orderPayload]) error { return nil },
		engine.Retry(events.RetryPolicy{MaxAttempts: 5, Backoff: events.Exponential, InitialDelay: time.Second}),
		engine.JobIdempotencyKey("event_id"))
}

func main() {}
`

func eventsFixtureSource(replacements ...string) string {
	src := strings.Replace(eventsFixtureMain, "import (", "import (\n\t\"time\"\n", 1)
	return strings.NewReplacer(replacements...).Replace(src)
}

func TestGenerate_EventsAndSubscriptionsProduceTheirManifestBlocks(t *testing.T) {
	dir := writeCollectFixture(t, eventsFixtureSource(), jobsFixtureManifest)
	ctx := generateCtx(t)

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := strings.Join(result.Blocks, ","); got != "emits,subscribes" {
		t.Errorf("Blocks = %s, want emits,subscribes", got)
	}

	got := readFile(t, filepath.Join(dir, "manifest.json"))
	for _, want := range []string{
		`"name": "widgets.widget.created"`, `"description": "A widget was created"`, `"widget_id": "string"`, `"note": "string?"`,
		`"handler": "handleConfirmedV1"`, `"async": false`,
		`"handler": "sales.order.confirmed.v2"`, `"idempotency_key_field": "event_id"`, `"backoff": "exponential"`, `"initial_delay_ms": 1000`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest.json lacks %s:\n%s", want, got)
		}
	}
	if strings.Count(got, `"name": "sales.order.confirmed"`) != 2 {
		t.Errorf("want one subscribes entry per registered version:\n%s", got)
	}
	manifest, err := readManifestJSON(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	emits, _ := manifest["emits"].([]any)
	if len(emits) != 1 {
		t.Errorf("emits = %v, want only the module's own event, not the one it defined to subscribe to", emits)
	}

	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Errorf("--check on fresh output: %v", err)
	}

	changed := eventsFixtureSource(`events.Description("A widget was created")`, `events.Description("A widget appeared"), events.Version(2)`)
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err == nil || !strings.Contains(err.Error(), "emits") {
		t.Fatalf("--check after changing the event = %v, want it to name emits", err)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); !strings.Contains(got, `"description": "A widget appeared"`) || !strings.Contains(got, `"version": 2`) {
		t.Errorf("manifest.json did not follow the changed definition:\n%s", got)
	}
}

func TestGenerate_TwoRegistrationsOfOneEventVersionFailGeneration(t *testing.T) {
	duplicated := eventsFixtureSource("engine.Subscribe(orderConfirmedV1, handleConfirmedV1, engine.Sync())",
		"engine.Subscribe(orderConfirmedV1, handleConfirmedV1)\n\tengine.Subscribe(orderConfirmedV1, handleConfirmedV1)")
	dir := writeCollectFixture(t, duplicated, jobsFixtureManifest)

	_, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), "sales.order.confirmed v1 is already subscribed") {
		t.Fatalf("Generate = %v, want the duplicate registration reported", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); got != jobsFixtureManifest {
		t.Errorf("manifest.json was written:\n%s", got)
	}
}
