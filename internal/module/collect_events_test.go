package module

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/events/def"
)

func TestEmitsCollector_OwnEventsOnlySortedByNameAndVersion(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindEvent: {
			def.EventDeclaration{Name: "widgets.widget.updated", Version: 2, Description: "Updated"},
			def.EventDeclaration{Name: "widgets.widget.created", Version: 1, PayloadSchema: map[string]any{"id": "string"}},
			def.EventDeclaration{Name: "widgets.widget.updated", Version: 1},
			def.EventDeclaration{Name: "sales.order.confirmed", Version: 1, Description: "defined to subscribe to it"},
			def.EventDeclaration{Name: "orm.record.created", Version: 1},
		},
	})

	want := `[{"name":"widgets.widget.created","version":1,"payload_schema":{"id":"string"}},` +
		`{"name":"widgets.widget.updated","version":1},` +
		`{"name":"widgets.widget.updated","version":2,"description":"Updated"}]`
	if got := collectJSON(t, emitsCollector{}, d, ModuleInfo{Name: "widgets"}); got != want {
		t.Errorf("emits =\n%s\nwant\n%s", got, want)
	}
}

func TestEmitsCollector_ASubscriberOnlyModuleEmitsNothing(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindEvent: {def.EventDeclaration{Name: "sales.order.confirmed", Version: 1}},
	})

	if got := collectJSON(t, emitsCollector{}, d, ModuleInfo{Name: "bridge"}); got != "[]" {
		t.Errorf("emits = %s, want none for a module that only subscribes", got)
	}
}

func TestEmitsCollector_SameDeclarationTwiceIsOneEntryButConflictsFail(t *testing.T) {
	same := def.EventDeclaration{Name: "widgets.widget.created", Version: 1, Description: "Created"}
	d := decls(t, map[string][]any{def.KindEvent: {same, same}})
	if got := collectJSON(t, emitsCollector{}, d, ModuleInfo{Name: "widgets"}); strings.Count(got, `"name"`) != 1 {
		t.Errorf("emits = %s, want one entry", got)
	}

	other := same
	other.Description = "Something else"
	d = decls(t, map[string][]any{def.KindEvent: {same, other}})
	if _, err := (emitsCollector{}).Collect(d, ModuleInfo{Name: "widgets"}); err == nil || !strings.Contains(err.Error(), "widgets.widget.created v1 is declared twice") {
		t.Errorf("conflicting declarations: %v", err)
	}
}

func TestSubscribesCollector_OneEntryPerRegisteredVersion(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindSubscription: {
			def.SubscriptionDeclaration{Event: "sales.order.confirmed", Version: 2, Async: true, Handler: "handleConfirmedV2"},
			def.SubscriptionDeclaration{
				Event: "sales.order.confirmed", Version: 1, Async: false, IdempotencyKeyField: "event_id", Handler: "handleConfirmedV1",
				RetryPolicy: &def.RetryPolicyDeclaration{MaxAttempts: 5, Backoff: "exponential", InitialDelayMS: 1000, MaxDelayMS: 60000, NoJitter: true},
			},
			def.SubscriptionDeclaration{Event: "contacts.contact.merged", Version: 1, Async: true, Transactional: true, Handler: "handleMerged"},
		},
	})

	want := `[{"name":"contacts.contact.merged","version":1,"handler":"handleMerged","async":true,"transactional":true},` +
		`{"name":"sales.order.confirmed","version":1,"handler":"handleConfirmedV1","async":false,"idempotency_key_field":"event_id",` +
		`"retry_policy":{"max_attempts":5,"backoff":"exponential","initial_delay_ms":1000,"max_delay_ms":60000,"jitter":false}},` +
		`{"name":"sales.order.confirmed","version":2,"handler":"handleConfirmedV2","async":true}]`
	if got := collectJSON(t, subscribesCollector{}, d, ModuleInfo{}); got != want {
		t.Errorf("subscribes =\n%s\nwant\n%s", got, want)
	}
}

func TestSubscribesCollector_TwoRegistrationsForOneVersionFail(t *testing.T) {
	sub := def.SubscriptionDeclaration{Event: "sales.order.confirmed", Version: 1, Async: true, Handler: "h"}
	d := decls(t, map[string][]any{def.KindSubscription: {sub, sub}})

	if _, err := (subscribesCollector{}).Collect(d, ModuleInfo{}); err == nil || !strings.Contains(err.Error(), "sales.order.confirmed v1 has more than one engine.Subscribe registration") {
		t.Errorf("duplicate registration: %v", err)
	}
}

func TestEmitsCollector_IdempotencyKeyFieldIsCarriedAndChecked(t *testing.T) {
	event := def.EventDeclaration{
		Name: "widgets.widget.created", Version: 1, IdempotencyKeyField: "widget_id",
		PayloadSchema: map[string]any{"widget_id": "string"},
	}
	got := collectJSON(t, emitsCollector{}, decls(t, map[string][]any{def.KindEvent: {event}}), ModuleInfo{Name: "widgets"})
	if !strings.Contains(got, `"idempotency_key_field":"widget_id"`) {
		t.Errorf("emits = %s, want idempotency_key_field carried", got)
	}

	event.IdempotencyKeyField = "missing"
	_, err := emitsCollector{}.Collect(decls(t, map[string][]any{def.KindEvent: {event}}), ModuleInfo{Name: "widgets"})
	if err == nil || !strings.Contains(err.Error(), `events.IdempotencyKeyField("missing") names no field of its payload type`) {
		t.Errorf("a key naming no payload field: %v", err)
	}

	mapPayload := def.EventDeclaration{Name: "widgets.widget.created", Version: 1, IdempotencyKeyField: "anything"}
	if _, err := (emitsCollector{}).Collect(decls(t, map[string][]any{def.KindEvent: {mapPayload}}), ModuleInfo{Name: "widgets"}); err != nil {
		t.Errorf("a payload whose keys are not known should not be checked: %v", err)
	}
}
