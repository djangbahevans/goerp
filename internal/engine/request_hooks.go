package engine

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

func hookInvocation(name string, req EngineRequest) registry.HookInvocation {
	return registry.HookInvocation{
		Hook: name, Request: req.Request,
		ContactID:   req.ContactID,
		Roles:       req.RolesLive,
		Permissions: req.PermissionSet,
	}
}

func (e *Engine) beforeRequestHooks(ctx context.Context, snap *registry.RegistrySnapshot, req EngineRequest) (EngineRequest, error) {
	for _, entry := range snap.Hooks(abiv1.HookRequestBefore) {
		result, err := snap.InvokeHook(ctx, e.wasmRuntime, entry, hookInvocation(abiv1.HookRequestBefore, req))
		if err != nil {
			return req, err
		}
		if result.Request == nil || result.Response != nil {
			return req, fmt.Errorf("before-request hook must return a request")
		}

		// Routing and identity are already authorized. Only content and presentation
		// changes cross the hook boundary, preventing a transform from bypassing checks.
		req.Body = result.Request.Body
		req.QueryParams = result.Request.QueryParams
		req.Headers = moduleRequestHeaders(http.Header(result.Request.Headers))
		req.Locale = result.Request.Locale
		req.Timezone = result.Request.Timezone
		req.Currency = result.Request.Currency
		req.Direction = result.Request.Direction
	}

	return req, nil
}

func (e *Engine) afterRequestHooks(ctx context.Context, snap *registry.RegistrySnapshot, req EngineRequest, resp EngineResponse) (EngineResponse, error) {
	for _, entry := range snap.Hooks(abiv1.HookRequestAfter) {
		invocation := hookInvocation(abiv1.HookRequestAfter, req)
		invocation.Response = &abiv1.Response{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body}
		result, err := snap.InvokeHook(ctx, e.wasmRuntime, entry, invocation)
		if err != nil {
			return resp, err
		}
		if result.Response == nil || result.Request != nil || result.Response.StatusCode < 200 || result.Response.StatusCode > 599 {
			return resp, fmt.Errorf("after-request hook must return a valid response")
		}

		if result.Response.StatusCode == resp.StatusCode && bytes.Equal(result.Response.Body, resp.Body) && result.Response.ErrorKind == "" {
			resp.Headers = result.Response.Headers
			continue
		}

		body, err := handlerResponseBody(*result.Response)
		if err != nil {
			return resp, err
		}
		wire := *result.Response
		if !bytes.Equal(body, resp.Body) {
			wire.Headers = maps.Clone(wire.Headers)
			maps.DeleteFunc(wire.Headers, func(name, _ string) bool { return strings.EqualFold(name, "Content-Length") })
		}
		resp = e.moduleResponse(wire, body, snap.Modules()[entry.Module], req.Locale)
	}

	return resp, nil
}

func (e *Engine) invokeModuleRequest(ctx context.Context, handlerName string, req EngineRequest, mod *module.LoadedModule) (EngineResponse, error) {
	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return EngineResponse{}, fmt.Errorf("borrow module instance: %w", err)
	}
	defer mod.Pool.Return(inst)

	return e.invokeHandler(ctx, inst, handlerName, req, mod)
}
