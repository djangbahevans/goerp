package engine

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type Request abi.Request

// PathParam returns the decoded value of a path parameter, or "" if absent.
func (r *Request) PathParam(name string) string {
	return r.PathParams[name]
}

// QueryParam returns the first value of a query key, or "" if absent.
func (r *Request) QueryParam(name string) string {
	return r.QueryParams.Get(name)
}

// QueryParamInt returns a query key's first value as an int, or def when
// the key is absent or its value is not an integer.
func (r *Request) QueryParamInt(name string, def int) int {
	n, err := strconv.Atoi(r.QueryParam(name))
	if err != nil {
		return def
	}
	return n
}

// QueryParamAll returns every value of a repeated query key, in request
// order, or nil if absent.
func (r *Request) QueryParamAll(name string) []string {
	return slices.Clone(r.QueryParams[name])
}

// Header returns the first value of a request header, matching name
// case-insensitively, or "" if absent.
func (r *Request) Header(name string) string {
	if values := r.Headers[strings.ToLower(name)]; len(values) > 0 {
		return values[0]
	}
	return ""
}

// HeaderAll returns every value of a request header, matching name
// case-insensitively, or nil if absent.
func (r *Request) HeaderAll(name string) []string {
	return slices.Clone(r.Headers[strings.ToLower(name)])
}

// RawBody returns the request body's exact bytes, for handlers registered
// with the RawBody() route option, such as webhook signature verification.
func (r *Request) RawBody() []byte {
	return r.Body
}

// ParseJSON unmarshals the request body into v with encoding/json/v2's
// defaults: invalid UTF-8 and duplicate member names are rejected, and
// member names match struct fields case-sensitively, so a name differing
// from its json tag only in case is ignored as unknown.
func (r *Request) ParseJSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("parse json body: %w", err)
	}
	return nil
}
