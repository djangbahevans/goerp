package main

import (
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/i18n"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

var deallocations uint32

func init() {
	for _, name := range []string{"append_a", "append_b", "append_c"} {
		engine.HandleHook(name, func(in engine.HookRequest) (engine.HookResult, error) {
			req := in.Request
			req.Body = append(req.Body, name[len(name)-1])
			req.UserID = "forged"
			req.TenantID = "forged"
			req.TraceID = "forged"
			req.Path = "/forged"
			req.Headers = map[string][]string{"Authorization": {"secret"}, "Cookie": {"secret"}, "X-Region": {"GH"}}
			return engine.HookResult{Request: &req}, nil
		})
	}
	engine.HandleHook("locale", func(in engine.HookRequest) (engine.HookResult, error) {
		req := in.Request
		if req.Locale != i18n.CurrentLocale() || req.Timezone != i18n.CurrentTimezone() || req.Currency != i18n.CurrentCurrency() || req.Direction != i18n.CurrentDirection() {
			return engine.HookResult{}, errors.New("hook locale differs from host")
		}
		req.Locale, req.Timezone, req.Currency, req.Direction = "he", "Asia/Jerusalem", "ILS", "rtl"
		return engine.HookResult{Request: &req}, nil
	})
	engine.HandleHook("native", func(in engine.HookRequest) (engine.HookResult, error) {
		req := in.Request
		req.Body = []byte(`{"name":"Hook widget","code":"hook-code"}`)
		return engine.HookResult{Request: &req}, nil
	})
	engine.HandleHook("authorized", func(in engine.HookRequest) (engine.HookResult, error) {
		allowed, err := authz.Check(perm.Ref("a:record:read"), "")
		if err != nil {
			return engine.HookResult{}, err
		}
		if !allowed {
			return engine.HookResult{}, errors.New("caller permission denied")
		}
		return engine.HookResult{Request: &in.Request}, nil
	})
	engine.HandleHook("reject", func(engine.HookRequest) (engine.HookResult, error) {
		return engine.HookResult{}, errors.New("fixture rejection")
	})
	engine.HandleHook("panic", func(engine.HookRequest) (engine.HookResult, error) {
		panic("fixture panic")
	})
	engine.HandleHook("after", func(in engine.HookRequest) (engine.HookResult, error) {
		response := *in.Response
		if response.Headers == nil {
			response.Headers = make(map[string]string)
		}
		response.Headers["X-Hook"] = "after"
		return engine.HookResult{Response: &response}, nil
	})
	engine.HandleHook("after_change", func(in engine.HookRequest) (engine.HookResult, error) {
		return engine.HookResult{Response: &engine.HookResponse{StatusCode: 202, Headers: map[string]string{"Content-Length": "1"}, Body: []byte(`{"changed":true}`)}}, nil
	})
	engine.HandleHook("after_error", func(in engine.HookRequest) (engine.HookResult, error) {
		return engine.HookResult{Response: &engine.HookResponse{StatusCode: 404, Body: in.Request.Body}}, nil
	})
	engine.HandleHook("record", func(in engine.HookRequest) (engine.HookResult, error) {
		fmt.Printf("HOOK:%s:%s:%s:%s:%s:%s:%s:%s\n", in.Hook, in.Module, in.OldVersion, in.Version, in.Request.TenantID, in.Request.TenantSlug, in.Request.UserID, in.Request.TraceID)
		return engine.HookResult{}, nil
	})
	engine.POST("/echo", func(req *engine.Request) *engine.Response {
		if req.Locale != i18n.CurrentLocale() || req.Timezone != i18n.CurrentTimezone() || req.Currency != i18n.CurrentCurrency() || req.Direction != i18n.CurrentDirection() {
			panic("request locale differs from host")
		}
		return engine.OK(map[string]string{
			"locale":        req.Locale,
			"timezone":      req.Timezone,
			"currency":      req.Currency,
			"direction":     req.Direction,
			"body":          string(req.Body),
			"user":          req.UserID,
			"tenant":        req.TenantID,
			"trace":         req.TraceID,
			"path":          req.Path,
			"authorization": req.Header("authorization"),
			"cookie":        req.Header("cookie"),
			"region":        req.Header("x-region"),
		})
	})
}

//go:wasmexport handle_hook
func handleHook(ptr, length uint32) uint64 { return engine.DispatchHook(ptr, length) }

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 { return engine.DispatchRequest(ptr, length) }

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModels() uint64 { return engine.WriteModels(model.Schema{}) }

//go:wasmexport get_data_migrations
func getMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { deallocations++; engine.Deallocate(ptr, size) }

//go:wasmexport deallocation_count
func deallocationCount() uint32 { return deallocations }

func main() {}
