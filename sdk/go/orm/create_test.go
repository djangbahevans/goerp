package orm

import "testing"

func TestCreateOptions_OnConflictIgnore_SetsPolicy(t *testing.T) {
	var o createOpts
	OnConflictIgnore("order_id", "warehouse_id")(&o)

	if o.OnConflict == nil {
		t.Fatal("OnConflict is nil")
	}
	if o.OnConflict.Policy != "ignore" {
		t.Errorf("Policy = %q, want %q", o.OnConflict.Policy, "ignore")
	}
	if len(o.OnConflict.Fields) != 2 || o.OnConflict.Fields[0] != "order_id" || o.OnConflict.Fields[1] != "warehouse_id" {
		t.Errorf("Fields = %v, want [order_id warehouse_id]", o.OnConflict.Fields)
	}
}

func TestCreateOptions_OnConflictUpdate_SetsPolicy(t *testing.T) {
	var o createOpts
	OnConflictUpdate("email")(&o)

	if o.OnConflict == nil {
		t.Fatal("OnConflict is nil")
	}
	if o.OnConflict.Policy != "update" {
		t.Errorf("Policy = %q, want %q", o.OnConflict.Policy, "update")
	}
	if len(o.OnConflict.Fields) != 1 || o.OnConflict.Fields[0] != "email" {
		t.Errorf("Fields = %v, want [email]", o.OnConflict.Fields)
	}
}
