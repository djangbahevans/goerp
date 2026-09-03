package modeltest

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/vmihailenco/msgpack/v5"
)

// Event is one host.event.emit/emit_tx emission, as observed by h.Events
// (§8 "Events") — decoded from the same event_delivery River job
// eventdelivery.Worker itself consumes to fan events out to subscribers.
type Event struct {
	ID               string
	Name             string
	Version          int
	EmitterModule    string
	Payload          map[string]any
	WasTransactional bool
	SyncDispatched   bool
	EmittedAt        time.Time
}

// TestEvents is h.Events — assertions against events the module under
// test emitted during the harness's lifetime (§8 "Events").
type TestEvents struct {
	t        *testing.T
	db       *sql.DB
	tenantID string
}

func newTestEvents(t *testing.T, db *sql.DB, tenantID string) *TestEvents {
	return &TestEvents{t: t, db: db, tenantID: tenantID}
}

// all returns every event_delivery job for this harness's tenant, in the
// order River assigned them ids — insertion order, since EventID is the
// only unique-constrained field and ids are a bigserial.
func (e *TestEvents) all() []Event {
	e.t.Helper()
	rows, err := e.db.Query(
		`SELECT args FROM river_job WHERE kind = 'event_delivery' AND args->>'tenant_id' = $1 ORDER BY id`,
		e.tenantID,
	)
	if err != nil {
		e.t.Fatalf("modeltest: query river_job: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var events []Event
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			e.t.Fatalf("modeltest: scan river_job.args: %v", err)
		}
		var args jobqueue.EventDeliveryArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			e.t.Fatalf("modeltest: unmarshal event_delivery args: %v", err)
		}
		var payload map[string]any
		if len(args.Payload) > 0 {
			if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
				e.t.Fatalf("modeltest: unmarshal event payload: %v", err)
			}
		}
		events = append(events, Event{
			ID:               args.EventID,
			Name:             args.EventName,
			Version:          args.EventVersion,
			EmitterModule:    args.EmitterModule,
			Payload:          payload,
			WasTransactional: args.Transactional,
			SyncDispatched:   args.SyncDispatched,
			EmittedAt:        args.EmittedAt,
		})
	}
	if err := rows.Err(); err != nil {
		e.t.Fatalf("modeltest: iterate river_job rows: %v", err)
	}
	return events
}

func (e *TestEvents) matching(name string) []Event {
	var out []Event
	for _, evt := range e.all() {
		if evt.Name == name {
			out = append(out, evt)
		}
	}
	return out
}

// AssertEmitted fails the test unless name was emitted at least once.
func (e *TestEvents) AssertEmitted(name string) {
	e.t.Helper()
	if len(e.matching(name)) == 0 {
		e.t.Fatalf("modeltest: expected event %q to have been emitted", name)
	}
}

// AssertNotEmitted fails the test if name was emitted at all.
func (e *TestEvents) AssertNotEmitted(name string) {
	e.t.Helper()
	if n := len(e.matching(name)); n != 0 {
		e.t.Fatalf("modeltest: expected event %q not to have been emitted, found %d", name, n)
	}
}

// AssertEmittedN fails the test unless name was emitted exactly n times.
func (e *TestEvents) AssertEmittedN(name string, n int) {
	e.t.Helper()
	if got := len(e.matching(name)); got != n {
		e.t.Fatalf("modeltest: event %q emitted %d times, want %d", name, got, n)
	}
}

// Last returns the most recently emitted event named name, or fails the
// test if it was never emitted.
func (e *TestEvents) Last(name string) *Event {
	e.t.Helper()
	matches := e.matching(name)
	if len(matches) == 0 {
		e.t.Fatalf("modeltest: expected event %q to have been emitted", name)
		return nil
	}
	return &matches[len(matches)-1]
}

// AssertEmittedInOrder fails the test unless every name in names was
// emitted, in that relative order (other events may be interleaved).
func (e *TestEvents) AssertEmittedInOrder(names ...string) {
	e.t.Helper()
	all := e.all()
	pos := 0
	for _, name := range names {
		found := false
		for ; pos < len(all); pos++ {
			if all[pos].Name == name {
				found = true
				pos++
				break
			}
		}
		if !found {
			e.t.Fatalf("modeltest: expected events %v in order, %q never found after its predecessor", names, name)
		}
	}
}
