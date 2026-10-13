// Command module exercises typed asynchronous handlers through the real host ABI.
package main

import (
	"errors"
	"strings"
	"time"

	_ "github.com/djangbahevans/goerp/sdk/go/config"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
	"github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture/schema"
)

func init() {
	engine.Subscribe(fixture.Received, receive)
	engine.Subscribe(fixture.ReceivedV2, receiveV2)
	engine.SubscribeTx(fixture.Committed, commit)
	engine.HandleJob(fixture.Process, process)

	engine.GET("/config", func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]any{
			"country":   fixture.Country.Get(),
			"enabled":   fixture.Enabled.Get(),
			"count":     fixture.Count.Get(),
			"ratio":     fixture.Ratio.Get(),
			"interval":  fixture.Interval.Get().String(),
			"countries": fixture.Countries.Get(),
			"settings":  fixture.SettingsValue.Get(),
			"counts":    fixture.Counts.Get(),
			"ratios":    fixture.Ratios.Get(),
			"secret":    fixture.Secret.Get(),
		})
	})
	engine.POST("/config", func(req *engine.Request) *engine.Response {
		if err := fixture.Country.Set(req.QueryParam("country")); err != nil {
			return engine.BadRequest(err)
		}
		return engine.OK(nil)
	})
}

func receive(evt events.Event[fixture.Payload]) error {
	if err := db.Insert("effects", effect(evt.Payload.Reference, "event", evt.ID, evt.TenantID, evt.UserID, "", 0)); err != nil {
		return err
	}
	if err := outcome(evt.Payload.Mode); err != nil {
		return events.PermanentError(err)
	}
	_, err := fixture.Process.Enqueue(evt.Payload)
	return err
}

func receiveV2(evt events.Event[fixture.Payload]) error {
	err := db.Insert("effects", effect(evt.Payload.Reference, "event-v2", evt.ID, evt.TenantID, evt.UserID, "", 0))
	return err
}

func commit(tx *db.Tx, evt events.Event[fixture.Payload]) error {
	if _, err := tx.Exec("INSERT INTO effects (tenant_id, reference, source, country, event_id, tenant_seen, user_seen) VALUES ($1, $2, $3, $4, $5, $6, $7)", evt.TenantID, evt.Payload.Reference, "transaction", fixture.Country.Get(), evt.ID, evt.TenantID, evt.UserID); err != nil {
		return err
	}
	if _, err := fixture.Process.EnqueueTx(tx, evt.Payload); err != nil {
		return err
	}
	if evt.Payload.Mode == "permanent" {
		return events.PermanentError(errors.New("permanent event failure"))
	}
	return outcome(evt.Payload.Mode)
}

func process(ctx *engine.JobContext, payload fixture.Payload) error {
	if strings.HasPrefix(payload.Mode, "progress") {
		ctx.ReportProgress(-10, "starting")
		ctx.ReportProgress(43, "imported")
		if err := ctx.SetResult(map[string]int{"created": 1}); err != nil {
			return err
		}
		if payload.Mode == "progress_fail" {
			return errors.New("private handler error")
		}
		if payload.Mode == "progress_large" {
			return ctx.SetResult(strings.Repeat("x", 256<<10))
		}
		return ctx.SetResult(map[string]int{"created": 2})
	}
	if payload.Mode == "permanent" {
		return jobs.PermanentError(errors.New("permanent job failure"))
	}
	if err := outcome(payload.Mode); err != nil {
		return err
	}
	err := db.Insert("effects", effect(payload.Reference, "job", "", ctx.TenantID, ctx.UserID, ctx.JobType, ctx.Attempt))
	return err
}

func outcome(mode string) error {
	switch mode {
	case "retry", "permanent":
		return errors.New("handler failed")
	case "sleep":
		time.Sleep(time.Hour)
	}
	return nil
}

type effectRecord struct {
	TenantID    string
	Reference   string
	Source      string
	Country     string
	EventID     string
	TenantSeen  string
	UserSeen    string
	TypeSeen    string
	Attempt     int
	RolesSeen   string
	ContactSeen string
}

func effect(reference, source, eventID, tenantID, userID, jobType string, attempt int) effectRecord {
	actor, err := db.QueryOne[struct {
		Roles   string `db:"roles"`
		Contact string `db:"contact"`
	}]("SELECT current_setting('app.current_user_roles', true) AS roles, current_setting('app.current_user_contact_id', true) AS contact", nil)
	if err != nil {
		panic(err)
	}

	return effectRecord{
		TenantID:    tenantID,
		Reference:   reference,
		Source:      source,
		Country:     fixture.Country.Get(),
		EventID:     eventID,
		TenantSeen:  tenantID,
		UserSeen:    userID,
		TypeSeen:    jobType,
		Attempt:     attempt,
		RolesSeen:   actor.Roles,
		ContactSeen: actor.Contact,
	}
}

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 { return engine.WriteModels(schema.Schema) }

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport handle_request
func handleRequest(ptr, size uint32) uint64 { return engine.DispatchRequest(ptr, size) }

//go:wasmexport handle_event
func handleEvent(ptr, size uint32) uint32 { return engine.DispatchEvent(ptr, size) }

//go:wasmexport handle_job
func handleJob(ptr, size uint32) uint32 { return engine.DispatchJob(ptr, size) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
