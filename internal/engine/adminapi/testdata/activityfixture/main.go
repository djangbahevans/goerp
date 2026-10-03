// Command activityfixture exercises workflow activities through the module SDK.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type reserveInput struct {
	OrderID string `msgpack:"order_id"`
}

type reserveOutput struct {
	ReservationID string `msgpack:"reservation_id"`
}

type widget struct{ Name string }

func (widget) ResourceName() string { return "activityfixture.widget" }

func (w *widget) Scan(row map[string]any) error {
	name, ok := row["name"].(string)
	if !ok {
		return orm.NewDecodeError("widget", "Name", "string", row["name"])
	}
	w.Name = name
	return nil
}

type identitySettings struct {
	UserID    string `db:"user_id" msgpack:"user_id"`
	ContactID string `db:"contact_id" msgpack:"contact_id"`
	Roles     string `db:"roles" msgpack:"roles"`
}

type readOutput struct {
	Settings identitySettings `msgpack:"settings"`
	SQLNames []string         `msgpack:"sql_names"`
	ORMNames []string         `msgpack:"orm_names"`
}

func init() {
	engine.OnActivity("reserve_inventory", func(ctx *engine.ActivityContext, in reserveInput) (reserveOutput, error) {
		if in.OrderID == "" {
			return reserveOutput{}, engine.WorkflowApplicationError("invalid_order", map[string]any{"reason": "order_id is required"})
		}
		return reserveOutput{ReservationID: "res-" + in.OrderID}, nil
	})
	engine.OnActivity("read_widgets", func(ctx *engine.ActivityContext, in struct{}) (readOutput, error) {
		var out readOutput
		settings, err := db.Query[identitySettings](`SELECT current_setting('app.current_user_id') AS user_id,
			current_setting('app.current_user_contact_id') AS contact_id,
			current_setting('app.current_user_roles') AS roles`, nil)
		if err != nil {
			return out, err
		}
		out.Settings = settings[0]
		rows, err := db.Query[widget]("SELECT name FROM widget ORDER BY name", nil)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			out.SQLNames = append(out.SQLNames, row.Name)
		}
		records, _, err := orm.From[widget]().All()
		if err != nil {
			return out, err
		}
		for _, record := range records {
			out.ORMNames = append(out.ORMNames, record.Name)
		}
		return out, nil
	})
	engine.OnActivity("open_transaction", func(ctx *engine.ActivityContext, trap bool) (struct{}, error) {
		if _, err := db.Begin(); err != nil {
			return struct{}{}, err
		}
		if trap {
			panic("activity trap after opening a transaction")
		}
		return struct{}{}, nil
	})
}

//go:wasmexport handle_activity
func handleActivity(ptr, length uint32) uint64 {
	return engine.DispatchActivity(ptr, length)
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
