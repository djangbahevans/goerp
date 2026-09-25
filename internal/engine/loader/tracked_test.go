package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestValidateTrackedFields_AcceptsColumnFieldsOnATableModel(t *testing.T) {
	md := model.Define("order").WithStandardFields().
		Field("state", model.Selection("draft", "confirmed").Tracked()).
		Field("customer_id", model.Many2One("contacts.contact").Tracked()).
		Field("total", model.Decimal(12, 2).Computed("compute_total").Store(true).Tracked())

	if err := validateTrackedFields([]model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("validateTrackedFields: %v", err)
	}
}

func TestValidateTrackedFields_RejectsInvalidPlacements(t *testing.T) {
	virtual := model.Define("remote").WithStandardFields().Field("state", model.Text().Tracked())
	virtual.Backend = model.BackendVirtual
	transient := model.Define("wizard").WithStandardFields().Field("state", model.Text().Tracked())
	transient.Backend = model.BackendTransient

	cases := map[string]struct {
		md   *model.ModelDeclaration
		want string
	}{
		"one2many": {
			model.Define("order").WithStandardFields().Field("lines", model.One2Many("sales.line", "order_id").Tracked()),
			"not valid on a One2Many field",
		},
		"non-stored computed": {
			model.Define("order").WithStandardFields().Field("total", model.Decimal(12, 2).Computed("compute_total").Tracked()),
			"not valid on a non-stored computed field",
		},
		"virtual model":   {virtual, "not valid on a virtual model"},
		"transient model": {transient, "not valid on a transient model"},
		"integer primary key": {
			model.Define("counter").Field("id", model.Integer().PrimaryKey()).Field("state", model.Text().Tracked()),
			"require a single UUID primary key",
		},
		"no primary key": {
			model.Define("counter").Field("state", model.Text().Tracked()),
			"require a single UUID primary key",
		},
	}
	for name, tc := range cases {
		err := validateTrackedFields([]model.ModelDeclaration{*tc.md})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", name, err, tc.want)
		}
	}
}

func TestValidateTrackedFields_IgnoresModelsWithoutTrackedFields(t *testing.T) {
	md := model.Define("counter").Field("id", model.Integer().PrimaryKey()).Field("state", model.Text())
	md.Backend = model.BackendVirtual

	if err := validateTrackedFields([]model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("validateTrackedFields: %v", err)
	}
}
