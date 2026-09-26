package wasm

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"maps"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const trackedTestUserID = "00000000-0000-0000-0000-0000000000aa"

// ticketModelDecl has tracked state/due_on/points and an untracked title.
func ticketModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "ticket",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "code", Def: model.Text()},
			{Name: "title", Def: model.Text()},
			{Name: "state", Def: model.Selection("open", "closed").Tracked()},
			{Name: "due_on", Def: model.Date().Tracked()},
			{Name: "points", Def: model.Integer().Tracked()},
		},
		Indexes: []model.NamedIndex{{Name: "idx_ticket_code", Def: model.BTreeIndex("code").Unique()}},
	}
}

type activityFixture struct {
	db   *sql.DB
	r    *Runtime
	mc   *ModuleContext
	slug string
}

func newActivityFixture(t *testing.T, userID string) *activityFixture {
	t.Helper()
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	slug := fmt.Sprintf("activitycapture%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE tenant_`+slug+`.ticket (
		id UUID PRIMARY KEY,
		code TEXT UNIQUE,
		title TEXT,
		state TEXT,
		due_on DATE,
		points INTEGER
	)`); err != nil {
		t.Fatalf("create ticket table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE tenant_`+slug+`.gadget (id UUID PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create gadget table: %v", err)
	}
	grantFixtureTables(t, primaryDB, slug, "ticket", "gadget")
	if err := recordactivity.NewStore(primaryDB).Bootstrap(ctx, slug); err != nil {
		t.Fatalf("record_activity Bootstrap: %v", err)
	}

	mc := NewModuleContext("req-1", "testmodule", userID, "", nil, nil, slug, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls: []model.ModelDeclaration{ticketModelDecl(), auditGadgetModelDecl()},
		})
	return &activityFixture{db: primaryDB, r: newHostDBTestRuntime(t, primaryDB, 10), mc: mc, slug: slug}
}

type activityRow struct {
	Model     string
	RecordID  string
	Kind      string
	Changes   []activityChange
	AuthorID  sql.NullString
	RequestID sql.NullString
	TraceID   sql.NullString
}

func (f *activityFixture) rows(t *testing.T) []activityRow {
	t.Helper()
	rows, err := f.db.Query(`SELECT model, record_id, kind, changes, author_id, request_id, trace_id FROM tenant_` + f.slug + `.record_activity ORDER BY id`)
	if err != nil {
		t.Fatalf("query record_activity: %v", err)
	}
	defer rows.Close()
	var out []activityRow
	for rows.Next() {
		var r activityRow
		var changes sql.NullString
		if err := rows.Scan(&r.Model, &r.RecordID, &r.Kind, &changes, &r.AuthorID, &r.RequestID, &r.TraceID); err != nil {
			t.Fatalf("scan record_activity: %v", err)
		}
		if changes.Valid {
			if err := json.Unmarshal([]byte(changes.String), &r.Changes); err != nil {
				t.Fatalf("decode changes: %v", err)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate record_activity rows: %v", err)
	}
	return out
}

func (f *activityFixture) createTicket(t *testing.T, id string, record map[string]any) {
	t.Helper()
	rec := map[string]any{"id": id}
	maps.Copy(rec, record)
	if _, hostErr := ORMCreate(t.Context(), f.r, f.db, f.r.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "testmodule.ticket", Record: rec}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}
}

func (f *activityFixture) write(t *testing.T, id string, record map[string]any) {
	t.Helper()
	if _, hostErr := ORMWrite(t.Context(), f.r, f.db, f.r.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "testmodule.ticket", ID: id, Record: record}); hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}
}

func changeByField(changes []activityChange) map[string]activityChange {
	out := make(map[string]activityChange, len(changes))
	for _, c := range changes {
		out[c.Field] = c
	}
	return out
}

const ticketA = "20000000-0000-0000-0000-000000000001"
const ticketB = "20000000-0000-0000-0000-000000000002"

func TestORMCreate_TrackedModel_WritesCreatedEntry(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"title": "Printer jam", "state": "open"})

	if _, hostErr := ORMCreate(t.Context(), f.r, f.db, f.r.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{
		Model: "testmodule.gadget", Record: map[string]any{"id": ticketB, "name": "untracked"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate gadget: %+v", hostErr)
	}

	rows := f.rows(t)
	if len(rows) != 1 {
		t.Fatalf("record_activity rows = %+v, want one created entry for the tracked model only", rows)
	}
	r := rows[0]
	if r.Model != "testmodule.ticket" || r.RecordID != ticketA || r.Kind != "created" || r.Changes != nil {
		t.Errorf("entry = %+v, want a created entry for %s", r, ticketA)
	}
	if r.AuthorID.String != trackedTestUserID || r.RequestID.String != "req-1" || r.TraceID.String != "trace-1" {
		t.Errorf("author/request/trace = %v/%v/%v, want the request's", r.AuthorID, r.RequestID, r.TraceID)
	}
}

func TestORMWrite_TrackedFields_WritesOneChangeEntryWithOldAndNewValues(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"title": "Printer jam", "state": "open", "due_on": "2026-09-25", "points": 3})

	f.write(t, ticketA, map[string]any{"state": "closed", "due_on": "2026-10-01", "title": "Printer jam (fixed)"})

	rows := f.rows(t)
	if len(rows) != 2 || rows[1].Kind != "change" {
		t.Fatalf("record_activity rows = %+v, want created then change", rows)
	}
	changes := changeByField(rows[1].Changes)
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want exactly state and due_on", rows[1].Changes)
	}
	if c := changes["state"]; c.Old != "open" || c.New != "closed" {
		t.Errorf("state change = %+v, want open → closed", c)
	}
	if c := changes["due_on"]; c.Old != "2026-09-25" || c.New != "2026-10-01" {
		t.Errorf("due_on change = %+v, want ISO dates 2026-09-25 → 2026-10-01", c)
	}
}

func TestORMWrite_UntrackedOrUnchangedFields_WriteNoEntry(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"title": "Printer jam", "state": "open"})

	f.write(t, ticketA, map[string]any{"title": "Printer jam, floor 2"})
	f.write(t, ticketA, map[string]any{"state": "open"})

	if rows := f.rows(t); len(rows) != 1 {
		t.Errorf("record_activity rows = %+v, want only the created entry", rows)
	}
}

func TestORMWrite_NoUserInContext_WritesNullAuthor(t *testing.T) {
	f := newActivityFixture(t, "")
	f.createTicket(t, ticketA, map[string]any{"state": "open"})
	f.write(t, ticketA, map[string]any{"state": "closed"})

	for _, r := range f.rows(t) {
		if r.AuthorID.Valid {
			t.Errorf("entry %s author_id = %q, want NULL for a write with no user", r.Kind, r.AuthorID.String)
		}
	}
}

func TestORMWrite_RolledBackTransaction_LeavesNoEntry(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"state": "open"})

	ctx := t.Context()
	const txID = "activity-tx"
	tx := registerTenantScopedTestTx(t, ctx, f.db, f.mc, txID)
	if _, hostErr := ORMWrite(ctx, f.r, f.db, f.r.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{
		Model: "testmodule.ticket", ID: ticketA, Record: map[string]any{"state": "closed"}, TxID: txID,
	}); hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if rows := f.rows(t); len(rows) != 1 {
		t.Errorf("record_activity rows = %+v, want only the created entry after a rolled-back write", rows)
	}
}

func TestORMWriteMany_WritesOneChangeEntryPerRecord(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"state": "open"})
	f.createTicket(t, ticketB, map[string]any{"state": "open"})

	if _, hostErr := ORMWriteMany(t.Context(), f.r, f.db, f.r.EventInsertClient(), f.mc, abiv1.ORMWriteManyInput{
		Model: "testmodule.ticket", IDs: []string{ticketA, ticketB}, Record: map[string]any{"state": "closed"},
	}); hostErr != nil {
		t.Fatalf("ORMWriteMany: %+v", hostErr)
	}

	changed := map[string]bool{}
	for _, r := range f.rows(t) {
		if r.Kind == "change" {
			changed[r.RecordID] = true
		}
	}
	if !changed[ticketA] || !changed[ticketB] || len(changed) != 2 {
		t.Errorf("change entries for %v, want one each for %s and %s", changed, ticketA, ticketB)
	}
}

func TestORMMutate_Increment_WritesChangeEntry(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"points": 3})

	if _, hostErr := ORMMutate(t.Context(), f.r, f.db, f.r.EventInsertClient(), f.mc, abiv1.ORMMutateInput{
		Model: "testmodule.ticket", ID: ticketA, Ops: []abiv1.ORMMutateOp{{Field: "points", Delta: 2}},
	}); hostErr != nil {
		t.Fatalf("ORMMutate: %+v", hostErr)
	}

	rows := f.rows(t)
	if len(rows) != 2 {
		t.Fatalf("record_activity rows = %+v, want created then change", rows)
	}
	c := changeByField(rows[1].Changes)["points"]
	if fmt.Sprint(c.Old) != "3" || fmt.Sprint(c.New) != "5" {
		t.Errorf("points change = %+v, want 3 → 5", c)
	}
}

func TestORMCreate_OnConflictUpdate_WritesChangeEntryForAnExistingRow(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"code": "T-1", "state": "open"})

	if _, hostErr := ORMCreate(t.Context(), f.r, f.db, f.r.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{
		Model:      "testmodule.ticket",
		Record:     map[string]any{"id": ticketB, "code": "T-1", "state": "closed"},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate upsert: %+v", hostErr)
	}

	rows := f.rows(t)
	if len(rows) != 2 || rows[1].Kind != "change" {
		t.Fatalf("record_activity rows = %+v, want created then change", rows)
	}
	if c := changeByField(rows[1].Changes)["state"]; c.Old != "open" || c.New != "closed" {
		t.Errorf("state change = %+v, want open → closed", c)
	}
}

func TestDBExec_TrackedUpdate_WritesChangeEntriesEvenWithSkipAudit(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"state": "open", "title": "a"})
	f.createTicket(t, ticketB, map[string]any{"state": "open", "title": "b"})
	ctx := t.Context()

	if _, hostErr := DBExec(ctx, f.db, f.mc, abiv1.DBExecInput{SQL: "UPDATE ticket SET state = $1", Params: []any{"closed"}, Opts: abiv1.DBExecOpts{SkipAudit: true}}); hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if _, hostErr := DBExec(ctx, f.db, f.mc, abiv1.DBExecInput{SQL: "UPDATE ticket SET title = $1", Params: []any{"renamed"}}); hostErr != nil {
		t.Fatalf("DBExec untracked: %+v", hostErr)
	}

	var changes []activityRow
	for _, r := range f.rows(t) {
		if r.Kind == "change" {
			changes = append(changes, r)
		}
	}
	if len(changes) != 2 {
		t.Fatalf("change entries = %+v, want one per row for the state update and none for the title update", changes)
	}
	for _, r := range changes {
		if c := changeByField(r.Changes)["state"]; c.Old != "open" || c.New != "closed" || len(r.Changes) != 1 {
			t.Errorf("record %s changes = %+v, want only state open → closed", r.RecordID, r.Changes)
		}
	}
}

func TestDBExecBatch_TrackedUpdate_WritesChangeEntries(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"state": "open"})
	f.createTicket(t, ticketB, map[string]any{"state": "open"})

	if _, hostErr := DBExecBatch(t.Context(), f.db, f.mc, abiv1.DBExecBatchInput{
		SQL:       "UPDATE ticket SET state = $1 WHERE id = $2",
		ParamSets: [][]any{{"closed", ticketA}, {"closed", ticketB}},
	}); hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}

	n := 0
	for _, r := range f.rows(t) {
		if r.Kind == "change" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("change entries = %d, want 2", n)
	}
}

func TestWriteChangeActivity_TrackedComputedField_ComparesAgainstTheStoredRow(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	ctx := t.Context()
	if _, err := f.db.ExecContext(ctx, `CREATE TABLE tenant_`+f.slug+`.invoice (id UUID PRIMARY KEY, total NUMERIC(12,2))`); err != nil {
		t.Fatalf("create invoice table: %v", err)
	}
	grantFixtureTables(t, f.db, f.slug, "invoice")
	if _, err := f.db.ExecContext(ctx, `INSERT INTO tenant_`+f.slug+`.invoice VALUES ($1, 10)`, ticketA); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}
	invoice := model.ModelDeclaration{Name: "invoice", Fields: []model.NamedField{
		{Name: "id", Def: model.UUID().PrimaryKey()},
		{Name: "total", Def: model.Decimal(12, 2).Computed("compute_total").Store(true).Tracked()},
	}}

	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyTenantScope(ctx, tx, f.mc); err != nil {
		t.Fatalf("apply tenant scope: %v", err)
	}
	stored, hostErr := fetchRowByPK(ctx, tx, invoice, "id", ticketA)
	if hostErr != nil {
		t.Fatalf("fetchRowByPK: %+v", hostErr)
	}
	// A compute function returning 10.0 for a stored 10.00 is not a change.
	recomputed := map[string]any{"id": ticketA, "total": 10.0}
	if hostErr := writeChangeActivity(ctx, tx, f.mc, "testmodule.invoice", invoice, stored, recomputed); hostErr != nil {
		t.Fatalf("writeChangeActivity: %+v", hostErr)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if rows := f.rows(t); len(rows) != 0 {
		t.Errorf("record_activity rows = %+v, want none for an unchanged computed value", rows)
	}
}

func TestDBExec_TrackedUpdateFrom_CapturesOnlyTheTargetTableOncePerRow(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"state": "open"})
	f.createTicket(t, ticketB, map[string]any{"state": "open"})
	ctx := t.Context()
	// staging.id collides with ticket.id, and ticketA matches two staging rows.
	if _, err := f.db.ExecContext(ctx, `CREATE TABLE tenant_`+f.slug+`.staging (id UUID, ticket_id UUID, new_state TEXT)`); err != nil {
		t.Fatalf("create staging table: %v", err)
	}
	grantFixtureTables(t, f.db, f.slug, "staging")
	if _, err := f.db.ExecContext(ctx, `INSERT INTO tenant_`+f.slug+`.staging VALUES (gen_random_uuid(), $1, 'closed'), (gen_random_uuid(), $1, 'closed')`, ticketA); err != nil {
		t.Fatalf("seed staging: %v", err)
	}

	out, hostErr := DBExec(ctx, f.db, f.mc, abiv1.DBExecInput{
		SQL:    "UPDATE ticket t SET state = s.new_state FROM staging s WHERE t.id = s.ticket_id AND s.new_state = $1",
		Params: []any{"closed"},
		Opts:   abiv1.DBExecOpts{Returning: "id"},
	})
	if hostErr != nil {
		t.Fatalf("DBExec UPDATE … FROM: %+v", hostErr)
	}
	if len(out.Returning) != 1 || out.Returning[0][0] != ticketA {
		t.Errorf("returning = %v, want only %s's own id", out.Returning, ticketA)
	}

	var changes []activityRow
	for _, r := range f.rows(t) {
		if r.Kind == "change" {
			changes = append(changes, r)
		}
	}
	if len(changes) != 1 || changes[0].RecordID != ticketA {
		t.Fatalf("change entries = %+v, want exactly one, for %s", changes, ticketA)
	}
	if c := changeByField(changes[0].Changes)["state"]; c.Old != "open" || c.New != "closed" {
		t.Errorf("state change = %+v, want open → closed", c)
	}
}
