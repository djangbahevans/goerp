// Package http makes outbound HTTPS requests through the engine's validated host.http.fetch capability.
package http

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type FetchRequest = abi.HTTPFetchInput
type FetchResponse = abi.HTTPFetchOutput

func Fetch(req FetchRequest) (FetchResponse, error) {
	var out FetchResponse

	err := hostcall.Do(hostHTTPFetch, req, &out)

	return out, err
}
