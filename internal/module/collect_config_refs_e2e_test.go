package module

import (
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/config/def"
)

func TestCollectDeclarations_ConfigRefsWithoutHostCalls(t *testing.T) {
	dir := writeCollectFixture(t, `package main

import (
	"time"
	"github.com/djangbahevans/goerp/sdk/go/config"
	"github.com/djangbahevans/goerp/sdk/go/config/def"
)

var Cert = config.Ref[string]("l10n_gh.vat_cert_number")
var Interval = def.Ref[time.Duration]("owner.interval")
var Company = config.CompanyName

func main() {}
`, collectFixtureManifest)

	decls, err := collectDeclarations(generateCtx(t), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := decodeDeclarations[def.RefDeclaration](decls, def.KindRef)
	if err != nil {
		t.Fatal(err)
	}

	want := []def.RefDeclaration{
		{Key: "l10n_gh.vat_cert_number", Type: def.TypeString},
		{Key: "owner.interval", Type: def.TypeDuration},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("references = %+v, want %+v", refs, want)
	}
}
