// Command ormcallerfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/wasm's own host.orm module-side caller tests
// (goerp#433) — it exercises all 10 sdk/go/orm functions
// (Create/Read/Write/Search/SearchRead/CreateBatch/FirstOrCreate/
// WriteMany/WriteWhere/Unlink) against a real "testmodule.widget" model,
// through the real sdk/go/orm package, rather than a hand-assembled
// bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o ormcallerfixture.wasm .
package main

import (
	"strconv"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

const widgetModel = "testmodule.widget"

// widget mirrors "testmodule.widget"'s own declared fields via db tags,
// the shape every orm.*[T] function here maps a record into.
type widget struct {
	ID    string `db:"id"`
	Name  string `db:"name"`
	Price int64  `db:"price"`
}

type stepResult struct {
	Step   string `msgpack:"step"`
	OK     bool   `msgpack:"ok"`
	Error  string `msgpack:"error,omitempty"`
	Detail string `msgpack:"detail,omitempty"`
}

type flowReport struct {
	Steps []stepResult `msgpack:"steps"`
}

func writeReport(r flowReport) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(flowReport{Steps: []stepResult{{Step: "marshal_report", Error: err.Error()}}})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport run_orm_flow
func runOrmFlow() uint64 {
	var report flowReport
	record := func(step string, detail string, err error) bool {
		sr := stepResult{Step: step, OK: err == nil, Detail: detail}
		if err != nil {
			sr.Error = err.Error()
		}
		report.Steps = append(report.Steps, sr)
		return err == nil
	}

	id1 := uuid.NewString()
	created, err := orm.Create[widget](widgetModel, map[string]any{"id": id1, "name": "Widget A", "price": int64(100)})
	if !record("create", created.Name, err) {
		return writeReport(report)
	}

	readOut, err := orm.Read[widget](widgetModel, []string{id1}, nil)
	record("read", strconv.Itoa(len(readOut)), err)

	err = orm.Write(widgetModel, id1, map[string]any{"price": int64(200)}, "")
	record("write", "", err)

	searchIDs, err := orm.Search(widgetModel, "record.price = 200")
	record("search", strconv.Itoa(len(searchIDs)), err)

	searchReadOut, _, err := orm.SearchRead[widget](widgetModel, "record.price = 200", []string{"id", "name", "price"})
	record("search_read", strconv.Itoa(len(searchReadOut)), err)

	id2, id3 := uuid.NewString(), uuid.NewString()
	batchOut, err := orm.CreateBatch[widget](widgetModel, []map[string]any{
		{"id": id2, "name": "Widget B", "price": int64(50)},
		{"id": id3, "name": "Widget C", "price": int64(50)},
	})
	record("create_batch", strconv.Itoa(len(batchOut)), err)

	focRecord, focCreated, err := orm.FirstOrCreate[widget](widgetModel,
		map[string]any{"name": "Widget A"},
		map[string]any{"id": uuid.NewString(), "price": int64(999)})
	_ = focRecord
	record("first_or_create", strconv.FormatBool(focCreated), err)

	writeManyOut, err := orm.WriteMany(widgetModel, []string{id2, id3}, map[string]any{"price": int64(300)})
	record("write_many", strconv.Itoa(writeManyOut.Count), err)

	writeWhereOut, err := orm.WriteWhere(widgetModel, "record.price = 300", map[string]any{"name": "Bulk"})
	record("write_where", strconv.Itoa(writeWhereOut.Count), err)

	unlinkOut, err := orm.Unlink(widgetModel, []string{id1})
	record("unlink", strconv.Itoa(unlinkOut.Count), err)

	return writeReport(report)
}

// runOrmTxFlow exercises the _Tx-suffixed transaction-participating
// counterparts (goerp#544) inside a single db.WithTx closure — the same
// shape go-sdk-reference.md §6a's own "Participating in a transaction"
// example uses — against a real compiled module and a real engine
// instance, proving the whole stack (SDK wrapper -> wire tx_id ->
// borrowed-transaction dispatch) round-trips correctly rather than just
// each layer in isolation.
//
//go:wasmexport run_orm_tx_flow
func runOrmTxFlow() uint64 {
	var report flowReport
	record := func(step string, detail string, err error) bool {
		sr := stepResult{Step: step, OK: err == nil, Detail: detail}
		if err != nil {
			sr.Error = err.Error()
		}
		report.Steps = append(report.Steps, sr)
		return err == nil
	}

	id1, id2 := uuid.NewString(), uuid.NewString()
	var searchCountInTx int64
	err := db.WithTx(func(tx *db.Tx) error {
		created, err := orm.CreateTx[widget](tx, widgetModel, map[string]any{"id": id1, "name": "Tx Widget A", "price": int64(700)})
		if err != nil {
			return err
		}
		record("create_tx", created.Name, nil)

		readBack, err := orm.ReadOneTx[widget](tx, widgetModel, id1, nil)
		if err != nil {
			return err
		}
		record("read_one_tx", readBack.Name, nil)

		searchCountInTx, err = orm.SearchCountTx(tx, widgetModel, "record.name = 'Tx Widget A'")
		if err != nil {
			return err
		}

		if err := orm.WriteTx(tx, widgetModel, id1, map[string]any{"price": int64(750)}, ""); err != nil {
			return err
		}
		record("write_tx", "", nil)

		batchOut, err := orm.CreateBatchTx[widget](tx, widgetModel, []map[string]any{
			{"id": id2, "name": "Tx Widget B", "price": int64(50)},
		})
		if err != nil {
			return err
		}
		record("create_batch_tx", strconv.Itoa(len(batchOut)), nil)

		writeManyOut, err := orm.WriteManyTx(tx, widgetModel, []string{id2}, map[string]any{"price": int64(60)})
		if err != nil {
			return err
		}
		record("write_many_tx", strconv.Itoa(writeManyOut.Count), nil)

		writeWhereOut, err := orm.WriteWhereTx(tx, widgetModel, "record.price = 60", map[string]any{"name": "Tx Widget B Renamed"})
		if err != nil {
			return err
		}
		record("write_where_tx", strconv.Itoa(writeWhereOut.Count), nil)

		unlinkOut, err := orm.UnlinkTx(tx, widgetModel, []string{id2})
		if err != nil {
			return err
		}
		record("unlink_tx", strconv.Itoa(unlinkOut.Count), nil)

		// Matches "name", which CreateTx already inserted on this same tx.
		_, focCreated, err := orm.FirstOrCreateTx[widget](tx, widgetModel,
			map[string]any{"name": "Tx Widget A"},
			map[string]any{"id": uuid.NewString(), "price": int64(999)})
		if err != nil {
			return err
		}
		record("first_or_create_tx", strconv.FormatBool(focCreated), nil)

		return nil
	})
	record("with_tx", strconv.FormatInt(searchCountInTx, 10), err)

	return writeReport(report)
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
