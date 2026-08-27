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
	created, err := orm.Create(orm.CreateInput{
		Model:  widgetModel,
		Record: map[string]any{"id": id1, "name": "Widget A", "price": int64(100)},
	})
	if !record("create", created.Record["name"].(string), err) {
		return writeReport(report)
	}

	readOut, err := orm.Read(orm.ReadInput{Model: widgetModel, IDs: []string{id1}})
	record("read", strconv.Itoa(len(readOut.Records)), err)

	_, err = orm.Write(orm.WriteInput{Model: widgetModel, ID: id1, Record: map[string]any{"price": int64(200)}})
	record("write", "", err)

	searchOut, err := orm.Search(orm.SearchInput{Model: widgetModel, Domain: "record.price = 200"})
	record("search", strconv.Itoa(len(searchOut.IDs)), err)

	searchReadOut, err := orm.SearchRead(orm.SearchReadInput{Model: widgetModel, Domain: "record.price = 200", Fields: []string{"id", "name", "price"}})
	record("search_read", strconv.Itoa(len(searchReadOut.Records)), err)

	id2, id3 := uuid.NewString(), uuid.NewString()
	batchOut, err := orm.CreateBatch(orm.CreateBatchInput{
		Model: widgetModel,
		Records: []map[string]any{
			{"id": id2, "name": "Widget B", "price": int64(50)},
			{"id": id3, "name": "Widget C", "price": int64(50)},
		},
	})
	record("create_batch", strconv.Itoa(len(batchOut.Records)), err)

	focOut, err := orm.FirstOrCreate(orm.FirstOrCreateInput{
		Model:  widgetModel,
		Domain: "record.name = 'Widget A'",
		Record: map[string]any{"id": uuid.NewString(), "name": "Widget A", "price": int64(999)},
	})
	record("first_or_create", strconv.FormatBool(focOut.Created), err)

	writeManyOut, err := orm.WriteMany(orm.WriteManyInput{Model: widgetModel, IDs: []string{id2, id3}, Record: map[string]any{"price": int64(300)}})
	record("write_many", strconv.Itoa(writeManyOut.Count), err)

	writeWhereOut, err := orm.WriteWhere(orm.WriteWhereInput{Model: widgetModel, Domain: "record.price = 300", Record: map[string]any{"name": "Bulk"}})
	record("write_where", strconv.Itoa(writeWhereOut.Count), err)

	unlinkOut, err := orm.Unlink(orm.UnlinkInput{Model: widgetModel, ID: id1})
	record("unlink", strconv.FormatBool(unlinkOut.Deleted), err)

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
