package db

import (
	"reflect"
	"testing"
)

func TestPatch_SetIfPresent_SkipsNil(t *testing.T) {
	var nilPtr *string
	p := NewPatch(nil)
	p.SetIfPresent("name", nilPtr)
	p.SetIfPresent("email", nil)

	if p.HasChanges() {
		t.Fatalf("HasChanges() = true, want false: %#v", p.ChangedFields())
	}
}

func TestPatch_ChangedFields_OrderAndOnlyNonNil(t *testing.T) {
	name := "Ada"
	isActive := true
	var email *string

	p := NewPatch(nil)
	p.SetIfPresent("name", &name)
	p.SetIfPresent("email", email)
	p.SetIfPresent("is_active", &isActive)

	want := []string{"name", "is_active"}
	if got := p.ChangedFields(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFields() = %v, want %v", got, want)
	}
	if !p.HasChanges() {
		t.Fatal("HasChanges() = false, want true")
	}
}

func TestPatch_ToUpdateSQL_ArgsMatchPlaceholders(t *testing.T) {
	name := "Ada"
	isActive := true

	p := NewPatch(nil)
	p.SetIfPresent("name", &name)
	p.SetIfPresent("is_active", &isActive)

	sql := p.ToUpdateSQL("contacts", "id-1")
	wantSQL := "UPDATE contacts SET name = $1, is_active = $2 WHERE id = $3"
	if sql != wantSQL {
		t.Fatalf("ToUpdateSQL() = %q, want %q", sql, wantSQL)
	}

	args := p.Args()
	if len(args) != 3 {
		t.Fatalf("Args() = %#v, want 3 values", args)
	}
	if *(args[0].(*string)) != "Ada" || *(args[1].(*bool)) != true || args[2] != "id-1" {
		t.Fatalf("Args() = %#v, want [Ada, true, id-1]", args)
	}
}

func TestPatch_NoChanges_NeverBuildsUpdate(t *testing.T) {
	p := NewPatch(nil)
	if p.HasChanges() {
		t.Fatal("HasChanges() = true on a fresh Patch, want false")
	}
	if got := p.Args(); len(got) != 0 {
		t.Fatalf("Args() = %#v, want empty", got)
	}
}

func TestPatch_ToUpdateSQL_PanicsWithNoChanges(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ToUpdateSQL() with no changes did not panic")
		}
	}()
	NewPatch(nil).ToUpdateSQL("contacts", "id-1")
}

func TestPatch_Args_PanicsBeforeToUpdateSQL(t *testing.T) {
	name := "Ada"
	defer func() {
		if recover() == nil {
			t.Fatal("Args() before ToUpdateSQL with pending changes did not panic")
		}
	}()
	p := NewPatch(nil)
	p.SetIfPresent("name", &name)
	p.Args()
}
