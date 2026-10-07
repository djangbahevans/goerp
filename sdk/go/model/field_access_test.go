package model

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func TestFieldAccess_RoundTripsThroughMsgpack(t *testing.T) {
	f := Integer().
		Access(
			AccessRead(perm.Ref("contacts:contact:financials_read")),
			AccessWrite(perm.Ref("contacts:contact:financials_write")),
		).
		OnDeniedRead(Omit)

	data, err := msgpack.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded FieldDef
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ReadPermission != "contacts:contact:financials_read" {
		t.Errorf("ReadPermission = %q, want %q", decoded.ReadPermission, "contacts:contact:financials_read")
	}
	if decoded.WritePermission != "contacts:contact:financials_write" {
		t.Errorf("WritePermission = %q, want %q", decoded.WritePermission, "contacts:contact:financials_write")
	}
	if decoded.DeniedRead == nil || decoded.DeniedRead.Kind != ReadKindOmit {
		t.Errorf("DeniedRead = %+v, want Kind ReadKindOmit", decoded.DeniedRead)
	}
	if decoded.DeniedWrite != nil {
		t.Errorf("DeniedWrite = %+v, want nil (not declared)", decoded.DeniedWrite)
	}
}

func TestFieldAccess_MaskStoresPattern(t *testing.T) {
	f := Char().
		Access(AccessRead(perm.Ref("hr:employee:banking_read"))).
		OnDeniedRead(Mask("****{last4}"))

	if f.DeniedRead == nil {
		t.Fatal("DeniedRead is nil")
	}
	if f.DeniedRead.Kind != ReadKindMask {
		t.Errorf("Kind = %v, want ReadKindMask", f.DeniedRead.Kind)
	}
	if f.DeniedRead.Pattern != "****{last4}" {
		t.Errorf("Pattern = %q, want %q", f.DeniedRead.Pattern, "****{last4}")
	}

	data, err := msgpack.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded FieldDef
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.DeniedRead == nil || decoded.DeniedRead.Pattern != "****{last4}" {
		t.Errorf("decoded DeniedRead = %+v, want Pattern %q", decoded.DeniedRead, "****{last4}")
	}
}

func TestFieldAccess_WriteOnlyProtection(t *testing.T) {
	// Write-only protection: no Read entry — anyone with record access
	// can read it, but writing requires the declared permission.
	f := Float().
		Access(AccessWrite(perm.Ref("sales:order:set_discount"))).
		OnDeniedWrite(Reject)

	if f.ReadPermission != "" {
		t.Errorf("ReadPermission = %q, want empty (no read restriction)", f.ReadPermission)
	}
	if f.DeniedRead != nil {
		t.Errorf("DeniedRead = %+v, want nil", f.DeniedRead)
	}
	if f.DeniedWrite == nil || *f.DeniedWrite != Reject {
		t.Errorf("DeniedWrite = %+v, want Reject", f.DeniedWrite)
	}
}

func TestFieldAccess_NoAccessCallHasNoRestriction(t *testing.T) {
	f := Char()

	if f.ReadPermission != "" || f.WritePermission != "" {
		t.Errorf("expected no permissions set, got read=%q write=%q", f.ReadPermission, f.WritePermission)
	}
	if f.DeniedRead != nil || f.DeniedWrite != nil {
		t.Errorf("expected no denied behaviours set, got DeniedRead=%+v DeniedWrite=%+v", f.DeniedRead, f.DeniedWrite)
	}
}

func TestAccessAndRequires_RejectTheZeroPermission(t *testing.T) {
	tests := map[string]func(){
		"AccessRead":  func() { AccessRead(perm.Permission{}) },
		"AccessWrite": func() { AccessWrite(perm.Permission{}) },
		"Requires":    func() { Transition("a", "b", "go").Requires(perm.Permission{}) },
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic on the zero perm.Permission")
				}
			}()
			fn()
		})
	}
}
