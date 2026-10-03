// Command identityfixture exercises job identity through real SDK host calls.
package main

import (
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type widget struct{ Name string }

func (widget) ResourceName() string { return "identityfixture.widget" }

func (w *widget) Scan(row map[string]any) error {
	name, ok := row["name"].(string)
	if !ok {
		return orm.NewDecodeError("widget", "Name", "string", row["name"])
	}

	w.Name = name

	return nil
}

type settings struct {
	UserID    string `db:"user_id"`
	ContactID string `db:"contact_id"`
	Roles     string `db:"roles"`
}

func init() {
	engine.OnJob("identity_read", func(ctx *engine.JobContext, _ struct{}) error {
		identity, err := db.Query[settings](`SELECT current_setting('app.current_user_id') AS user_id,
			current_setting('app.current_user_contact_id') AS contact_id,
			current_setting('app.current_user_roles') AS roles`, nil)
		if err != nil {
			return err
		}

		rows, err := db.Query[widget]("SELECT name FROM widget ORDER BY name", nil)
		if err != nil {
			return err
		}

		var sqlNames, ormNames []string
		for _, row := range rows {
			sqlNames = append(sqlNames, row.Name)
		}

		records, _, err := orm.From[widget]().All()
		if err != nil {
			return err
		}

		for _, record := range records {
			ormNames = append(ormNames, record.Name)
		}

		slices.Sort(ormNames)

		if _, err := db.Exec("UPDATE widget SET seen = seen + 1"); err != nil {
			return err
		}

		_, err = db.Exec("INSERT INTO delivery_audit (user_id, contact_id, roles, sql_names, orm_names, envelope_user_id) VALUES ($1, $2, $3, $4, $5, $6)",
			identity[0].UserID, identity[0].ContactID, identity[0].Roles, strings.Join(sqlNames, ","), strings.Join(ormNames, ","), ctx.UserID)

		return err
	})

	engine.OnJob("identity_enqueue", func(_ *engine.JobContext, p struct {
		Transactional bool `msgpack:"transactional"`
	}) error {
		// Delaying fixture jobs prevents other clients from claiming them before
		// the test executes them directly.
		opts := []jobs.JobOption{
			jobs.WithDelay(time.Hour),
			jobs.WithIdempotencyKey("identity-read"),
		}

		if !p.Transactional {
			_, err := jobs.Enqueue("identity_read", struct{}{}, opts...)
			return err
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}

		defer tx.Rollback()

		if _, err := jobs.EnqueueTx(tx, "identity_read", struct{}{}, opts...); err != nil {
			return err
		}

		return tx.Commit()
	})
}

//go:wasmexport handle_job
func handleJob(ptr, length uint32) uint32 { return engine.DispatchJob(ptr, length) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
