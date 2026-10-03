package wasm

import (
	"fmt"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestExecReturning_BorrowedTransaction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		rows      int
		batch     bool
	}{
		{name: "single insert", operation: "INSERT", rows: 1},
		{name: "single update", operation: "UPDATE", rows: 1},
		{name: "single delete", operation: "DELETE", rows: 1},
		{name: "sequential insert", operation: "INSERT", rows: 2, batch: true},
		{name: "copy insert", operation: "INSERT", rows: 101, batch: true},
		{name: "pipeline update", operation: "UPDATE", rows: 2, batch: true},
		{name: "pipeline delete", operation: "DELETE", rows: 2, batch: true},
	} {
		for _, valid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/valid=%t", tc.name, valid), func(t *testing.T) {
				primary, slug, mc := setupExecTest(t)
				ctx := t.Context()
				paramSets := make([][]any, tc.rows)
				for i := range tc.rows {
					id := fmt.Sprintf("70000000-0000-0000-0000-%012d", i+1)
					paramSets[i] = []any{id, fmt.Sprintf("changed-%d", i)}
					if tc.operation != "INSERT" {
						if _, err := primary.ExecContext(ctx, "INSERT INTO tenant_"+slug+".widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", id, fmt.Sprintf("original-%d", i)); err != nil {
							t.Fatalf("seed widget: %v", err)
						}
					}
				}

				statement := "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)"
				if tc.name == "copy insert" {
					statement = "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)"
					for i, params := range paramSets {
						paramSets[i] = []any{params[0], "70000000-0000-0000-0000-0000000000aa", params[1]}
					}
				} else if tc.operation == "UPDATE" {
					statement = "UPDATE widget SET name = $2 WHERE id = $1"
				} else if tc.operation == "DELETE" {
					statement = "DELETE FROM widget WHERE id = $1 AND $2::text IS NOT NULL"
				}

				const txID = "returning-tx"
				tx := registerTenantScopedTestTx(t, ctx, primary, mc, txID)
				t.Cleanup(func() { _ = tx.Rollback() })
				if _, hostErr := DBExec(ctx, primary, mc, abiv1.DBExecInput{
					SQL: "INSERT INTO gadget (id, name) VALUES (gen_random_uuid(), 'caller write')", TxID: txID,
				}); hostErr != nil {
					t.Fatalf("caller write: %+v", hostErr)
				}

				returning := "id, no_such_column"
				if valid {
					returning = "id, name"
				}

				var (
					hostErr *abiv1.HostError
					rows    [][]any
				)
				if tc.batch {
					p, prepareErr := prepareExec(statement, abiv1.DBExecOpts{Returning: returning}, mc)
					if prepareErr != nil {
						t.Fatalf("prepare batch: %+v", prepareErr)
					}
					if tc.name == "copy insert" && !resolveCopyPlan(p, tc.rows, mc).Eligible {
						t.Fatal("fixture must exercise COPY")
					}
					if tc.operation != "INSERT" && !pipelineEligible(p, paramSets) {
						t.Fatal("fixture must exercise pipelining")
					}

					out, err := DBExecBatch(ctx, primary, mc, abiv1.DBExecBatchInput{
						SQL: statement, ParamSets: paramSets, TxID: txID, Opts: abiv1.DBExecBatchOpts{Returning: returning},
					})
					rows, hostErr = out.Returning, err
				} else {
					out, err := DBExec(ctx, primary, mc, abiv1.DBExecInput{
						SQL: statement, Params: paramSets[0], TxID: txID, Opts: abiv1.DBExecOpts{Returning: returning},
					})
					rows, hostErr = out.Returning, err
				}

				if valid {
					if hostErr != nil {
						t.Fatalf("valid returning: %+v", hostErr)
					}
					if len(rows) != tc.rows {
						t.Fatalf("returning rows = %d, want %d", len(rows), tc.rows)
					}
					for i, row := range rows {
						name := fmt.Sprintf("changed-%d", i)
						if tc.operation == "DELETE" {
							name = fmt.Sprintf("original-%d", i)
						}
						if len(row) != 2 || row[0] != paramSets[i][0] || row[1] != name {
							t.Errorf("returning[%d] = %v, want [%s %s]", i, row, paramSets[i][0], name)
						}
					}
				} else {
					code := abiv1.ErrCodeExecError
					message := `opts.returning: column "no_such_column" does not exist`
					if tc.batch {
						code = abiv1.ErrCodeDBBatchError
						if tc.name != "copy insert" {
							message = "parameter set 0: " + message
						}
					}
					if hostErr == nil || hostErr.Code != code || hostErr.Message != message {
						t.Fatalf("invalid returning error = %+v, want %s with missing-column message", hostErr, code)
					}
				}

				if err := tx.Commit(); err != nil {
					t.Fatalf("commit caller transaction: %v", err)
				}

				wantRows := tc.rows
				if (tc.operation == "INSERT" && !valid) || (tc.operation == "DELETE" && valid) {
					wantRows = 0
				}
				var count int
				if err := primary.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil || count != wantRows {
					t.Fatalf("persisted widgets = %d, want %d: %v", count, wantRows, err)
				}
				if tc.operation == "UPDATE" {
					if err := primary.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget WHERE name LIKE 'changed-%'").Scan(&count); err != nil {
						t.Fatalf("count changed widgets: %v", err)
					}
					wantChanged := 0
					if valid {
						wantChanged = tc.rows
					}
					if count != wantChanged {
						t.Errorf("changed widgets = %d, want %d", count, wantChanged)
					}
				}

				wantAudit := 0
				if valid {
					wantAudit = tc.rows
				}
				if err := primary.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".audit_log").Scan(&count); err != nil || count != wantAudit {
					t.Errorf("audit rows = %d, want %d: %v", count, wantAudit, err)
				}
				if err := primary.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".gadget WHERE name = 'caller write'").Scan(&count); err != nil || count != 1 {
					t.Errorf("caller writes = %d, want 1: %v", count, err)
				}
			})
		}
	}
}

func TestExecReturning_ContinueOnError(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("borrowed=%t", borrowed), func(t *testing.T) {
			primary, slug, mc := setupExecTest(t)
			ctx := t.Context()
			txID := ""
			commit := func() error { return nil }
			if borrowed {
				txID = "partial-returning-tx"
				tx := registerTenantScopedTestTx(t, ctx, primary, mc, txID)
				t.Cleanup(func() { _ = tx.Rollback() })
				commit = tx.Commit
			}

			_, hostErr := DBExecBatch(ctx, primary, mc, abiv1.DBExecBatchInput{
				SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES (gen_random_uuid(), gen_random_uuid(), $1)",
				ParamSets: [][]any{{"first"}, {"second"}}, TxID: txID,
				Opts: abiv1.DBExecBatchOpts{Returning: "missing", ContinueOnError: true},
			})
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeDBBatchPartialError || hostErr.Details["failed_count"] != 2 || hostErr.Details["total_rows_affected"] != 0 {
				t.Fatalf("partial batch error = %+v, want two failures and no writes", hostErr)
			}
			if err := commit(); err != nil {
				t.Fatalf("commit caller transaction: %v", err)
			}

			var count int
			if err := primary.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil || count != 0 {
				t.Errorf("persisted widgets = %d, want 0: %v", count, err)
			}
		})
	}
}

func TestExecReturning_PhysicalColumns(t *testing.T) {
	primary, slug, mc := setupExecTest(t)
	ctx := t.Context()
	if _, err := primary.ExecContext(ctx, "ALTER TABLE tenant_"+slug+`.widget ADD COLUMN extra TEXT DEFAULT 'physical column'`); err != nil {
		t.Fatalf("add undeclared column: %v", err)
	}

	out, hostErr := DBExec(ctx, primary, mc, abiv1.DBExecInput{
		SQL:  "INSERT INTO widget (id, tenant_id, name) VALUES (gen_random_uuid(), gen_random_uuid(), 'widget')",
		Opts: abiv1.DBExecOpts{Returning: "extra"},
	})
	if hostErr != nil {
		t.Fatalf("return physical column: %+v", hostErr)
	}
	if len(out.Returning) != 1 || len(out.Returning[0]) != 1 || out.Returning[0][0] != "physical column" {
		t.Errorf("returning = %v, want [[physical column]]", out.Returning)
	}
}

func TestExecReturning_TrackedUpdateRejectsBeforeWrite(t *testing.T) {
	f := newActivityFixture(t, trackedTestUserID)
	f.createTicket(t, ticketA, map[string]any{"state": "open"})
	ctx := t.Context()
	const txID = "tracked-returning-tx"
	tx := registerTenantScopedTestTx(t, ctx, f.db, f.mc, txID)
	t.Cleanup(func() { _ = tx.Rollback() })

	_, hostErr := DBExec(ctx, f.db, f.mc, abiv1.DBExecInput{
		SQL: "UPDATE ticket SET state = 'closed' WHERE id = $1", Params: []any{ticketA}, TxID: txID,
		Opts: abiv1.DBExecOpts{Returning: "missing"},
	})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeExecError {
		t.Fatalf("invalid returning error = %+v, want db.exec_error", hostErr)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit caller transaction: %v", err)
	}

	var state string
	if err := f.db.QueryRowContext(ctx, "SELECT state FROM tenant_"+f.slug+".ticket WHERE id = $1", ticketA).Scan(&state); err != nil || state != "open" {
		t.Errorf("persisted state = %q, want open: %v", state, err)
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].Kind != "created" {
		t.Errorf("record activity = %+v, want only the creation entry", rows)
	}
}
