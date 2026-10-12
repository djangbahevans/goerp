// Command module checks locale fields against host and SDK values.
package main

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/i18n"
	"github.com/djangbahevans/goerp/sdk/go/model"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/localefixture"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmimport host.i18n get_locale
func hostLocale(ptr, size uint32) uint64

func current() abi.LocaleInfo {
	packed := hostLocale(0, 0)
	ptr, size := uint32(packed>>32), uint32(packed)
	defer engine.Deallocate(ptr, size)
	var envelope abi.Envelope
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, size), &envelope); err != nil {
		panic(err)
	}
	if !envelope.OK {
		panic(envelope.Error)
	}
	var info abi.LocaleInfo
	if err := msgpack.Unmarshal(envelope.Data, &info); err != nil {
		panic(err)
	}
	return info
}

func check(info, want abi.LocaleInfo) error {
	if got := current(); got != info {
		return fmt.Errorf("host = %+v, fields = %+v", got, info)
	}
	if info != want {
		return fmt.Errorf("fields = %+v, expected = %+v", info, want)
	}
	if i18n.CurrentLocale() != info.Locale || i18n.CurrentTimezone() != info.Timezone || i18n.CurrentCurrency() != info.Currency || i18n.CurrentDirection() != info.Direction {
		return fmt.Errorf("SDK differs from fields")
	}
	return nil
}

func init() {
	engine.GET("/locale", func(req *engine.Request) *engine.Response {
		info := abi.LocaleInfo{Locale: req.Locale, Timezone: req.Timezone, Currency: req.Currency, Direction: req.Direction}
		if err := check(info, info); err != nil {
			panic(err)
		}
		return engine.OK(map[string]string{
			"locale":    info.Locale,
			"timezone":  info.Timezone,
			"currency":  info.Currency,
			"direction": info.Direction,
		})
	}, engine.Auth(engine.AuthOptional))
	engine.HandleJob(fixture.Process, func(ctx *engine.JobContext, want abi.LocaleInfo) error {
		return check(abi.LocaleInfo{Locale: ctx.Locale, Timezone: ctx.Timezone, Currency: ctx.Currency, Direction: ctx.Direction}, want)
	})
	engine.Subscribe(fixture.Received, func(e events.Event[abi.LocaleInfo]) error {
		return check(abi.LocaleInfo{Locale: e.Locale, Timezone: e.Timezone, Currency: e.Currency, Direction: e.Direction}, e.Payload)
	})
}

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModels() uint64 { return engine.WriteModels(model.Schema{}) }

//go:wasmexport get_data_migrations
func getMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport handle_request
func handleRequest(ptr, size uint32) uint64 { return engine.DispatchRequest(ptr, size) }

//go:wasmexport handle_job
func handleJob(ptr, size uint32) uint32 { return engine.DispatchJob(ptr, size) }

//go:wasmexport handle_event
func handleEvent(ptr, size uint32) uint32 { return engine.DispatchEvent(ptr, size) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }
func main()                       {}
