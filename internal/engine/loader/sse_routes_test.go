package loader

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestSSEPollIntervalRouteValidation(t *testing.T) {
	for _, interval := range []int{0, 499, 500, 2000, 60000, 60001} {
		err := validateModuleRoutes(&manifest.Manifest{Type: "domain"}, []abi.RouteDeclaration{{Method: "GET", Path: "/stream", Streaming: true, PollIntervalMs: interval}})
		if (err == nil) != (interval >= 500 && interval <= 60000) {
			t.Fatalf("interval=%d err=%v", interval, err)
		}
	}
	for _, rt := range []abi.RouteDeclaration{
		{Method: "POST", Path: "/stream", Streaming: true, PollIntervalMs: 2000},
		{Method: "GET", Path: "/ordinary", PollIntervalMs: 2000},
		{Method: "GET", Path: "/socket", Websocket: true, PollIntervalMs: 2000},
	} {
		if err := validateModuleRoutes(&manifest.Manifest{Type: "domain"}, []abi.RouteDeclaration{rt}); err == nil {
			t.Fatalf("accepted %+v", rt)
		}
	}
}
