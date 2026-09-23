// Command ormcallerfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/wasm's own host.orm module-side caller tests
// (goerp#433) — it exercises the typed sdk/go/orm v2 surface (goerp#982)
// against a real "testmodule.widget" model, through the real sdk/go/orm
// package, rather than a hand-assembled bytecode stand-in.
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

// Widget mirrors "testmodule.widget"'s own declared fields (widgetModelDecl,
// internal/engine/wasm/host_orm_test.go) in the same shape goerp module
// generate emits onto a real generated model struct — hand-written here
// since this fixture predates the generator and isn't its output, but
// price stays *int32 (not required) to match the declared field exactly.
type Widget struct {
	ID    string
	Name  string
	Price *int32
}

func (Widget) ResourceName() string { return "testmodule.widget" }

var WidgetFields = struct {
	ID    orm.Field[Widget, string]
	Name  orm.StringField[Widget]
	Price orm.OrderedField[Widget, int32]
}{
	ID:    orm.NewField[Widget, string]("id"),
	Name:  orm.NewStringField[Widget]("name"),
	Price: orm.NewOrderedField[Widget, int32]("price"),
}

var WidgetAllFields = []orm.AnyField[Widget]{WidgetFields.ID, WidgetFields.Name, WidgetFields.Price}

// Scan implements sdk/go/orm's reflection-free decode contract
// (goerp#974), the same shape goerp module generate emits (goerp#977).
func (w *Widget) Scan(row map[string]any) error {
	if v, ok := row["id"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Widget", "ID", "string", v)
		}
		w.ID = s
	}
	if v, ok := row["name"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Widget", "Name", "string", v)
		}
		w.Name = s
	}
	if v, ok := row["price"]; ok && v != nil {
		n, ok := v.(int64)
		if !ok {
			return orm.NewDecodeError("Widget", "Price", "*int32", v)
		}
		converted := int32(n)
		w.Price = &converted
	}
	return nil
}

// WidgetValues is a typed builder for Widget's writable fields — the same
// shape goerp module generate emits (goerp#978).
type WidgetValues struct {
	orm.Values[Widget]
}

func NewWidgetValues() *WidgetValues {
	return &WidgetValues{Values: *orm.NewValues[Widget]()}
}

func (v *WidgetValues) SetID(x string) *WidgetValues {
	orm.Set(&v.Values, WidgetFields.ID, x)
	return v
}

func (v *WidgetValues) SetName(x string) *WidgetValues {
	orm.Set(&v.Values, WidgetFields.Name.Field, x)
	return v
}

func (v *WidgetValues) SetPrice(x int32) *WidgetValues {
	orm.Set(&v.Values, WidgetFields.Price.Field, x)
	return v
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

// priceOf reads *w.Price, or -1 if unset — every step below sets it
// before reading it back, so this only guards against a real decode bug
// rather than a legitimately-absent value.
func priceOf(w Widget) int64 {
	if w.Price == nil {
		return -1
	}
	return int64(*w.Price)
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
	createVals := NewWidgetValues().SetID(id1).SetName("Widget A").SetPrice(100)
	created, err := orm.Create[Widget](&createVals.Values)
	if !record("create", created.Name, err) {
		return writeReport(report)
	}

	readOut, err := orm.GetMany[Widget]([]string{id1})
	record("read", strconv.Itoa(len(readOut)), err)

	writeVals := NewWidgetValues().SetPrice(200)
	err = orm.Write[Widget](id1, &writeVals.Values, nil)
	record("write", "", err)

	searchIDs, err := orm.From[Widget]().Where(WidgetFields.Price.Eq(200)).IDs()
	record("search", strconv.Itoa(len(searchIDs)), err)

	searchReadOut, _, err := orm.From[Widget]().Where(WidgetFields.Price.Eq(200)).Select(WidgetAllFields...).All()
	record("search_read", strconv.Itoa(len(searchReadOut)), err)

	id2, id3 := uuid.NewString(), uuid.NewString()
	batchVals2 := NewWidgetValues().SetID(id2).SetName("Widget B").SetPrice(50)
	batchVals3 := NewWidgetValues().SetID(id3).SetName("Widget C").SetPrice(50)
	batchOut, err := orm.CreateBatch[Widget]([]*orm.Values[Widget]{&batchVals2.Values, &batchVals3.Values})
	record("create_batch", strconv.Itoa(len(batchOut)), err)

	focUnique := NewWidgetValues().SetName("Widget A")
	focCreate := NewWidgetValues().SetID(uuid.NewString()).SetPrice(999)
	focRecord, focCreated, err := orm.FirstOrCreate[Widget](&focUnique.Values, &focCreate.Values)
	_ = focRecord
	record("first_or_create", strconv.FormatBool(focCreated), err)

	writeManyVals := NewWidgetValues().SetPrice(300)
	writeManyOut, err := orm.WriteMany[Widget]([]string{id2, id3}, &writeManyVals.Values)
	record("write_many", strconv.Itoa(writeManyOut.Count), err)

	writeWhereVals := NewWidgetValues().SetName("Bulk")
	writeWhereOut, err := orm.WriteWhere[Widget](WidgetFields.Price.Eq(300).Bind(), &writeWhereVals.Values)
	record("write_where", strconv.Itoa(writeWhereOut.Count), err)

	// Three widgets exist at this point: id1=200, id2=300, id3=300.
	count, err := orm.Count[Widget](orm.MatchAll[Widget]())
	record("count", strconv.FormatInt(count, 10), err)

	sum, err := orm.Sum(WidgetFields.Price, orm.MatchAll[Widget]())
	record("sum", strconv.FormatFloat(sum, 'f', 0, 64), err)

	min, err := orm.Min[Widget](WidgetFields.Price, orm.MatchAll[Widget]())
	record("min", strconv.FormatFloat(min, 'f', 0, 64), err)

	max, err := orm.Max[Widget](WidgetFields.Price, orm.MatchAll[Widget]())
	record("max", strconv.FormatFloat(max, 'f', 0, 64), err)

	avg, err := orm.Avg(WidgetFields.Price, orm.MatchAll[Widget]())
	record("avg", strconv.FormatFloat(avg, 'f', 2, 64), err)

	mutated, err := orm.Mutate[Widget](id2, orm.Decrement(WidgetFields.Price.Field, int32(50)), orm.Where(WidgetFields.Price.Gte(50)))
	record("mutate", strconv.FormatInt(priceOf(mutated), 10), err)

	_, err = orm.Mutate[Widget](id2, orm.Decrement(WidgetFields.Price.Field, int32(1000)), orm.Where(WidgetFields.Price.Gte(1000)))
	if orm.IsPreconditionFailed(err) {
		err = nil
	}
	record("mutate_guard", "", err)

	unlinkOut, err := orm.Unlink[Widget](id1)
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
// count_tx is the only in-tx count taken (old pre-#975 orm distinguished
// an aggregate-based CountTx from a search-based SearchCountTx; v2's only
// transaction-participating count is Query.Tx(tx).Count(), so the two
// collapsed into one call) — its result is also reused for with_tx's own
// detail after the transaction commits.
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
	var countInTx int64
	err := db.WithTx(func(tx *db.Tx) error {
		createVals := NewWidgetValues().SetID(id1).SetName("Tx Widget A").SetPrice(700)
		created, err := orm.CreateTx[Widget](tx, &createVals.Values)
		if err != nil {
			return err
		}
		record("create_tx", created.Name, nil)

		readBack, err := orm.GetTx[Widget](tx, id1)
		if err != nil {
			return err
		}
		record("read_one_tx", readBack.Name, nil)

		countInTx, err = orm.From[Widget]().Tx(tx).Where(WidgetFields.Name.Eq("Tx Widget A")).Count()
		if err != nil {
			return err
		}
		record("count_tx", strconv.FormatInt(countInTx, 10), nil)

		writeVals := NewWidgetValues().SetPrice(750)
		if err := orm.WriteTx[Widget](tx, id1, &writeVals.Values, nil); err != nil {
			return err
		}
		record("write_tx", "", nil)

		batchVals := NewWidgetValues().SetID(id2).SetName("Tx Widget B").SetPrice(50)
		batchOut, err := orm.CreateBatchTx[Widget](tx, []*orm.Values[Widget]{&batchVals.Values})
		if err != nil {
			return err
		}
		record("create_batch_tx", strconv.Itoa(len(batchOut)), nil)

		writeManyVals := NewWidgetValues().SetPrice(60)
		writeManyOut, err := orm.WriteManyTx[Widget](tx, []string{id2}, &writeManyVals.Values)
		if err != nil {
			return err
		}
		record("write_many_tx", strconv.Itoa(writeManyOut.Count), nil)

		writeWhereVals := NewWidgetValues().SetName("Tx Widget B Renamed")
		writeWhereOut, err := orm.WriteWhereTx[Widget](tx, WidgetFields.Price.Eq(60).Bind(), &writeWhereVals.Values)
		if err != nil {
			return err
		}
		record("write_where_tx", strconv.Itoa(writeWhereOut.Count), nil)

		mutated, err := orm.MutateTx[Widget](tx, id2, orm.Increment(WidgetFields.Price.Field, int32(5)))
		if err != nil {
			return err
		}
		record("mutate_tx", strconv.FormatInt(priceOf(mutated), 10), nil)

		unlinkOut, err := orm.UnlinkTx[Widget](tx, id2)
		if err != nil {
			return err
		}
		record("unlink_tx", strconv.Itoa(unlinkOut.Count), nil)

		// Matches "name", which CreateTx already inserted on this same tx.
		focUnique := NewWidgetValues().SetName("Tx Widget A")
		focCreate := NewWidgetValues().SetID(uuid.NewString()).SetPrice(999)
		_, focCreated, err := orm.FirstOrCreateTx[Widget](tx, &focUnique.Values, &focCreate.Values)
		if err != nil {
			return err
		}
		record("first_or_create_tx", strconv.FormatBool(focCreated), nil)

		return nil
	})
	record("with_tx", strconv.FormatInt(countInTx, 10), err)

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
