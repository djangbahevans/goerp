package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestValidateReservedTableNames_RejectsEngineOwnedTableNames(t *testing.T) {
	var names []string
	for _, g := range enginetables.Groups {
		for _, tbl := range g.Tables {
			names = append(names, tbl.Name)
			if tbl.Partitioned {
				names = append(names, tbl.Name+"_p20260901")
			}
		}
	}
	for _, name := range names {
		explicit := model.Define("widget", model.Table(name)).WithStandardFields()
		derived := model.Define(name).WithStandardFields()
		for _, md := range []*model.ModelDeclaration{explicit, derived} {
			err := validateReservedTableNames([]model.ModelDeclaration{*md})
			if err == nil || !strings.Contains(err.Error(), `"`+name+`"`) {
				t.Errorf("model %s (table %q): err = %v, want a rejection naming the table", md.Name, name, err)
			}
		}
	}
}

func TestValidateReservedTableNames_RejectsDerivedNameMatch(t *testing.T) {
	md := model.Define("RecordShares").WithStandardFields()
	err := validateReservedTableNames([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), `"record_shares"`) {
		t.Errorf("err = %v, want a rejection naming record_shares", err)
	}
}

func TestValidateReservedTableNames_RejectsSystemCatalogAndPlannedNames(t *testing.T) {
	for _, name := range []string{"pg_settings", "pg_widgets", "view_overrides", "notifications"} {
		md := model.Define("widget", model.Table(name)).WithStandardFields()
		if err := validateReservedTableNames([]model.ModelDeclaration{*md}); err == nil {
			t.Errorf("table %q: want a rejection, got nil", name)
		}
	}
}

func TestValidateReservedTableNames_AllowsOrdinaryAndNonTableModels(t *testing.T) {
	virtual := model.Define("roles").WithStandardFields()
	virtual.Backend = model.BackendVirtual
	transient := model.Define("audit_log").WithStandardFields()
	transient.Backend = model.BackendTransient
	models := []model.ModelDeclaration{
		*model.Define("contacts.contact").WithStandardFields(),
		*model.Define("widget", model.Table("roles_catalog")).WithStandardFields(),
		*model.Define("AuditLogEntry").WithStandardFields(),
		*virtual,
		*transient,
	}
	if err := validateReservedTableNames(models); err != nil {
		t.Errorf("validateReservedTableNames: %v", err)
	}
}
