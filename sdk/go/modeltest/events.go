package modeltest

import (
	"database/sql"
	"encoding/json/v2"
	"slices"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/vmihailenco/msgpack/v5"
)

// Event describes a committed emission with its payload decoded into P.
type Event[P any] struct {
	ID               string
	Name             string
	Version          int
	EmitterModule    string
	Payload          P
	WasTransactional bool
	SyncDispatched   bool
	EmittedAt        time.Time
}

// TestEvents asserts against committed emissions belonging to the harness tenant.
// Assertions match both event name and version and do not run delivery workers.
type TestEvents struct {
	t        failer
	db       *sql.DB
	tenantID string
}

func newTestEvents(t failer, db *sql.DB, tenantID string) *TestEvents {
	return &TestEvents{t: t, db: db, tenantID: tenantID}
}

func (e *TestEvents) all() []Event[[]byte] {
	e.t.Helper()

	rows, err := e.db.Query(
		`SELECT args FROM system.river_job WHERE kind = 'event_delivery' AND args->>'tenant_id' = $1 ORDER BY id`,
		e.tenantID,
	)
	if err != nil {
		e.t.Fatalf("modeltest: query emitted events: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var result []Event[[]byte]
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			e.t.Fatalf("modeltest: read emitted event: %v", err)
			return nil
		}

		var args jobqueue.EventDeliveryArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			e.t.Fatalf("modeltest: decode emitted event envelope: %v", err)
			return nil
		}

		result = append(result, Event[[]byte]{
			ID:               args.EventID,
			Name:             args.EventName,
			Version:          args.EventVersion,
			EmitterModule:    args.EmitterModule,
			Payload:          args.Payload,
			WasTransactional: args.Transactional,
			SyncDispatched:   args.SyncDispatched,
			EmittedAt:        args.EmittedAt,
		})
	}
	if err := rows.Err(); err != nil {
		e.t.Fatalf("modeltest: iterate emitted events: %v", err)
	}

	return result
}

func (e *TestEvents) matching(def events.Definition) []Event[[]byte] {
	e.t.Helper()

	var result []Event[[]byte]
	for _, evt := range e.all() {
		if evt.Name == def.Name() && evt.Version == def.Version() {
			result = append(result, evt)
		}
	}

	return result
}

// AssertEmitted fails the test unless def's name and version were emitted.
func (e *TestEvents) AssertEmitted(def events.Definition) {
	e.t.Helper()

	if len(e.matching(def)) == 0 {
		e.t.Fatalf("modeltest: expected event %q v%d to have been emitted", def.Name(), def.Version())
	}
}

// AssertNotEmitted fails the test if def's name and version were emitted.
func (e *TestEvents) AssertNotEmitted(def events.Definition) {
	e.t.Helper()

	e.AssertEmittedN(def, 0)
}

// AssertEmittedN fails the test unless def's name and version were emitted n times.
func (e *TestEvents) AssertEmittedN(def events.Definition, n int) {
	e.t.Helper()

	if got := len(e.matching(def)); got != n {
		e.t.Fatalf("modeltest: event %q v%d emitted %d times, want %d", def.Name(), def.Version(), got, n)
	}
}

// Last returns def's most recent committed emission with its payload decoded into P.
// It fails the test if no matching name and version exists or the payload cannot decode.
func (e *TestEvents) Last[P any](def events.Def[P]) *Event[P] {
	e.t.Helper()

	matches := e.matching(def)
	if len(matches) == 0 {
		e.t.Fatalf("modeltest: expected event %q v%d to have been emitted", def.Name(), def.Version())
		return nil
	}
	last := matches[len(matches)-1]

	var payload P
	if err := msgpack.Unmarshal(last.Payload, &payload); err != nil {
		e.t.Fatalf("modeltest: decode event %q v%d payload: %v", def.Name(), def.Version(), err)
		return nil
	}

	return &Event[P]{
		ID:               last.ID,
		Name:             last.Name,
		Version:          last.Version,
		EmitterModule:    last.EmitterModule,
		Payload:          payload,
		WasTransactional: last.WasTransactional,
		SyncDispatched:   last.SyncDispatched,
		EmittedAt:        last.EmittedAt,
	}
}

// AssertEmittedInOrder fails the test unless defs' names and versions were emitted
// in relative insertion order. Other emissions may be interleaved.
func (e *TestEvents) AssertEmittedInOrder(defs ...events.Definition) {
	e.t.Helper()

	remaining := e.all()
	for _, def := range defs {
		index := slices.IndexFunc(remaining, func(evt Event[[]byte]) bool {
			return evt.Name == def.Name() && evt.Version == def.Version()
		})
		if index == -1 {
			e.t.Fatalf("modeltest: expected event %q v%d after its predecessor", def.Name(), def.Version())
			return
		}
		remaining = remaining[index+1:]
	}
}
