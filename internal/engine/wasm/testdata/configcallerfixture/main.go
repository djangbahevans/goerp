// Command configcallerfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's host.config module-side test — it reads and writes
// typed config definitions through the real sdk/go/config package, rather
// than a hand-assembled stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o configcallerfixture.wasm .
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
)

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
