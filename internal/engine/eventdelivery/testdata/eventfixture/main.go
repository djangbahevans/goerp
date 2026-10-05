// Command eventfixture exercises event handler host calls through the module SDK.
package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type widget struct{ Name string }

func (widget) ResourceName() string { return "eventfixture.widget" }

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

type happenedPayload struct {
	Mode string `msgpack:"mode"`
}

var happened = events.Define[happenedPayload]("test.event.happened")

func init() {
	engine.Subscribe(happened, func(evt events.Event[happenedPayload]) error {
		input := evt.Payload

		if input.Mode != "read" {
			if _, err := db.Begin(); err != nil {
				return err
			}

			switch input.Mode {
			case "trap":
				panic("event trap after opening a transaction")
			case "retry":
				return fmt.Errorf("retry event")
			case "permanent":
				return events.PermanentError(fmt.Errorf("permanent event"))
			default:
				return nil
			}
		}

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

		_, err = db.Exec("INSERT INTO delivery_audit (user_id, contact_id, roles, sql_names, orm_names) VALUES ($1, $2, $3, $4, $5)",
			identity[0].UserID, identity[0].ContactID, identity[0].Roles, strings.Join(sqlNames, ","), strings.Join(ormNames, ","))

		return err
	})
}

//go:wasmexport handle_event
func handleEvent(ptr, length uint32) uint32 { return engine.DispatchEvent(ptr, length) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
