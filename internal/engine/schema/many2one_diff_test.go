package schema

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDiffAndExecute_Many2One_CreatesForeignKeyConstraint(t *testing.T) {
	sess, engine := setupTenantSchema(t, "many2onetest")
	conn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{
		*model.Define("contact", model.Table("contacts")).
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order", model.Table("orders")).
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("testmodule.contact").OnDelete(model.SetNull)),
	}

	changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if !tableExists(t, conn, "tenant_many2onetest", "orders") {
		t.Fatal("orders table was not created")
	}
	if !columnExists(t, conn, "tenant_many2onetest", "orders", "customer_id") {
		t.Fatal("customer_id column was not created")
	}

	var constraintCount int
	if err := conn.QueryRow(`
		SELECT count(*) FROM information_schema.table_constraints
		WHERE table_schema = 'tenant_many2onetest' AND table_name = 'orders'
		AND constraint_type = 'FOREIGN KEY'
	`).Scan(&constraintCount); err != nil {
		t.Fatalf("query foreign key constraints: %v", err)
	}
	if constraintCount != 1 {
		t.Fatalf("foreign key constraint count = %d, want 1", constraintCount)
	}

	// Prove the constraint is real by exercising it: an insert referencing
	// a nonexistent contact must be rejected by Postgres itself.
	_, err = conn.Exec(`INSERT INTO tenant_many2onetest.orders (id, customer_id) VALUES (gen_random_uuid(), gen_random_uuid())`)
	if err == nil {
		t.Fatal("expected a foreign key violation inserting a nonexistent customer_id, got none")
	}
}
