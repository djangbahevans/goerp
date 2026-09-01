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

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
