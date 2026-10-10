// Command configcallerfixture exercises the config SDK through WASM host calls.
package main

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/config"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

type result struct {
	OK           bool     `msgpack:"ok"`
	Error        string   `msgpack:"error,omitempty"`
	Country      string   `msgpack:"country,omitempty"`
	CountrySet   bool     `msgpack:"country_set,omitempty"`
	Interval     int64    `msgpack:"interval,omitempty"`
	Threshold    float64  `msgpack:"threshold,omitempty"`
	Currencies   []string `msgpack:"currencies,omitempty"`
	APIKey       string   `msgpack:"api_key,omitempty"`
	StrangerFail bool     `msgpack:"stranger_fail,omitempty"`
}

var (
	defaultCountry = config.String("default_country_code", "GH", config.Label("Default Country"))
	reconcileEvery = config.Duration("reconcile_interval", 15*time.Minute, config.Label("Reconcile Interval"))
	dedupThreshold = config.Float("dedup_threshold", 0.85, config.Label("Threshold"), config.Min(0), config.Max(1))
	currencies     = config.StringSlice("currencies", []string{"GHS"}, config.Label("Currencies"))
	apiKey         = config.String("api_key", "", config.Label("API Key"), config.Required(), config.Encrypted())
	undeclared     = config.String("not_in_manifest", "", config.Label("Undeclared"))
	vatCert        = config.Ref[string]("l10n_gh.vat_cert_number")
	softFlag       = config.Ref[bool]("soft_mod.flag")
)

type readOnlyResult struct {
	VATCert     string `msgpack:"vat_cert"`
	VATFound    bool   `msgpack:"vat_found"`
	SoftFlag    bool   `msgpack:"soft_flag"`
	SoftFound   bool   `msgpack:"soft_found"`
	CompanyName string `msgpack:"company_name"`
	Address     string `msgpack:"address"`
	TaxID       string `msgpack:"tax_id"`
	LogoURL     string `msgpack:"logo_url"`
	LogoFound   bool   `msgpack:"logo_found"`
}

//go:wasmexport run_read_only
func runReadOnly() uint64 {
	_, vatFound := vatCert.Lookup()
	_, softFound := softFlag.Lookup()
	_, logoFound := config.CompanyLogoURL.Lookup()
	data, err := msgpack.Marshal(readOnlyResult{
		VATCert:     vatCert.Get(),
		VATFound:    vatFound,
		SoftFlag:    softFlag.Get(),
		SoftFound:   softFound,
		CompanyName: config.CompanyName.Get(),
		Address:     config.CompanyAddress.Get(),
		TaxID:       config.CompanyTaxID.Get(),
		LogoURL:     config.CompanyLogoURL.Get(),
		LogoFound:   logoFound,
	})
	if err != nil {
		panic(err)
	}

	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)

	return uint64(ptr)<<32 | uint64(len(data))
}

func writeResult(r result) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(result{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport run_read
func runRead() uint64 {
	_, set := defaultCountry.Lookup()
	return writeResult(result{
		OK:         true,
		Country:    defaultCountry.Get(),
		CountrySet: set,
		Interval:   int64(reconcileEvery.Get()),
		Threshold:  dedupThreshold.Get(),
		Currencies: currencies.Get(),
		APIKey:     apiKey.Get(),
	})
}

//go:wasmexport run_write
func runWrite() uint64 {
	if err := defaultCountry.Set("NG"); err != nil {
		return writeResult(result{Error: "country: " + err.Error()})
	}
	if err := reconcileEvery.Set(90 * time.Second); err != nil {
		return writeResult(result{Error: "interval: " + err.Error()})
	}
	if err := currencies.Set([]string{"USD", "EUR"}); err != nil {
		return writeResult(result{Error: "currencies: " + err.Error()})
	}
	return writeResult(result{OK: true, StrangerFail: undeclared.Set("x") != nil})
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
