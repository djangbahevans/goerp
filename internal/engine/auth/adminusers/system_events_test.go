package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type systemEventRecorder struct {
	args []event.EventDeliveryArgs
	err  error
}

func (s *systemEventRecorder) InsertTx(_ context.Context, _ *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	s.args = append(s.args, args.(event.EventDeliveryArgs))
	return nil, s.err
}

func TestMemberTransitions_SystemEvents(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "operator", "Operator", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	recorder := &systemEventRecorder{}
	store := e.handler.store
	store.SystemEvents = event.NewSystemEmitter(e.conn, recorder)
	audit := authaudit.Row{EventType: "user.suspended", TenantID: ft.id, UserID: target, ActorUserID: admin, Success: true, Metadata: []byte(`{"reason":"policy"}`)}
	if err := store.suspend(t.Context(), ft.slug, target, admin, "policy", audit); err != nil {
		t.Fatal(err)
	}
	audit.EventType = "user.removed"
	if err := store.remove(t.Context(), ft.slug, target, audit); err != nil {
		t.Fatal(err)
	}
	expected := []map[string]any{
		{"user_id": target, "reason": "policy", "suspended_by": admin, "scope": "tenant"},
		{"user_id": target, "removed_by": admin},
	}
	if len(recorder.args) != len(expected) {
		t.Fatalf("events = %+v", recorder.args)
	}
	for i, args := range recorder.args {
		var payload map[string]any
		if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if args.TenantID != ft.id || args.EventVersion != 0 || !reflect.DeepEqual(payload, expected[i]) {
			t.Errorf("event = %+v, payload = %#v", args, payload)
		}
	}
}

func TestMemberTransition_SystemEventFailureRollsBack(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "operator", "Operator", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	failure := errors.New("queue unavailable")
	e.handler.store.SystemEvents = event.NewSystemEmitter(e.conn, &systemEventRecorder{err: failure})
	audit := authaudit.Row{EventType: "user.suspended", TenantID: ft.id, UserID: target, ActorUserID: admin, Success: true, Metadata: []byte(`{"reason":"policy"}`)}
	if err := e.handler.store.suspend(t.Context(), ft.slug, target, admin, "policy", audit); !errors.Is(err, failure) {
		t.Fatalf("suspend error = %v", err)
	}
	if got := e.memberStatus(t, ft, target); got != "active" {
		t.Fatalf("member status = %s after queue failure", got)
	}
}
