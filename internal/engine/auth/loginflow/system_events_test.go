package loginflow

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type loginEventRecorder struct{ args []event.EventDeliveryArgs }

func (s *loginEventRecorder) InsertTx(_ context.Context, _ *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	s.args = append(s.args, args.(event.EventDeliveryArgs))
	return nil, nil
}

func TestFailedLogin_SystemEvent(t *testing.T) {
	f := newFixture(t)
	recorder := &loginEventRecorder{}
	f.handler.SystemEvents = event.NewSystemEmitter(f.conn, recorder)
	response := f.doLogin(t, map[string]any{"email": f.email, "password": "incorrect password", "tenant": f.tenantSlug}, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if len(recorder.args) != 1 {
		t.Fatalf("failed login events = %+v", recorder.args)
	}
	args := recorder.args[0]
	var payload map[string]any
	if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"email": f.email, "tenant_id": f.tenantID, "ip": f.remoteIP, "reason": "invalid_credentials"}
	if args.EventName != "system.user.login_failed" || args.TenantID != f.tenantID || !reflect.DeepEqual(payload, want) {
		t.Fatalf("event = %+v, payload = %#v, want %#v", args, payload, want)
	}
}
