package modeltest

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/djangbahevans/goerp/internal/engine"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// QueryOption adds a query-string parameter to a request (§6 "Request
// options"). Built with modeltest.WithQuery.
type QueryOption struct {
	key, value string
}

// WithQuery adds a ?key=value query-string parameter to a request.
func WithQuery(key, value string) QueryOption {
	return QueryOption{key: key, value: value}
}

// requestState is the per-call configuration h.GET/h.POST/etc read, built
// up by AsUser/WithPermissions/WithRoles/Anonymous/WithHeader (§6 "Request
// options"). Every method returns a new *Harness-shaped view rather than
// mutating h itself, so h.AsUser(...) never affects a later h.GET call
// made without it.
type requestState struct {
	h         *Harness
	userID    string
	perms     permission.PermissionBitfield
	roles     []string
	anonymous bool
	headers   map[string]string
}

func (h *Harness) baseState() *requestState {
	return &requestState{
		h:      h,
		userID: h.UserID,
		perms:  h.allPerms,
	}
}

func (s *requestState) clone() *requestState {
	c := *s
	c.headers = make(map[string]string, len(s.headers))
	maps.Copy(c.headers, s.headers)
	return &c
}

// AsUser makes userID the caller for this request chain, keeping the
// harness's default all-permissions grant unless WithPermissions/
// WithRoles narrows it.
func (h *Harness) AsUser(userID string) *requestState {
	s := h.baseState()
	s.userID = userID
	return s
}

// WithPermissions overrides the caller's permissions for this request
// chain, regardless of which user is making it.
func (h *Harness) WithPermissions(perms ...string) *requestState {
	return h.baseState().WithPermissions(perms...)
}

// WithRoles overrides the caller's roles for this request chain — distinct
// from permissions, needed for ABAC conditions that call user_has_role(...).
func (h *Harness) WithRoles(roles ...string) *requestState {
	return h.baseState().WithRoles(roles...)
}

// Anonymous makes this request chain unauthenticated, for public routes.
func (h *Harness) Anonymous() *requestState {
	return h.baseState().Anonymous()
}

// WithHeader sets a request header for this request chain.
func (h *Harness) WithHeader(key, value string) *requestState {
	return h.baseState().WithHeader(key, value)
}

func (s *requestState) AsUser(userID string) *requestState {
	c := s.clone()
	c.userID = userID
	return c
}

func (s *requestState) WithPermissions(perms ...string) *requestState {
	c := s.clone()
	c.perms = permissionsToBitfield(s.h.permReg, perms)
	return c
}

func (s *requestState) WithRoles(roles ...string) *requestState {
	c := s.clone()
	c.roles = roles
	return c
}

func (s *requestState) Anonymous() *requestState {
	c := s.clone()
	c.anonymous = true
	return c
}

func (s *requestState) WithHeader(key, value string) *requestState {
	c := s.clone()
	c.headers[key] = value
	return c
}

// GET/POST/PUT/PATCH/DELETE — see the *Harness methods below for the
// zero-configuration entry points; these are the same methods available
// after AsUser/WithPermissions/WithRoles/Anonymous/WithHeader/WithLocale.

func (s *requestState) GET(path string, opts ...QueryOption) *Response {
	return s.do(http.MethodGet, path, nil, opts)
}

func (s *requestState) POST(path string, body any, opts ...QueryOption) *Response {
	return s.do(http.MethodPost, path, body, opts)
}

func (s *requestState) PUT(path string, body any, opts ...QueryOption) *Response {
	return s.do(http.MethodPut, path, body, opts)
}

func (s *requestState) PATCH(path string, body any, opts ...QueryOption) *Response {
	return s.do(http.MethodPatch, path, body, opts)
}

func (s *requestState) DELETE(path string, opts ...QueryOption) *Response {
	return s.do(http.MethodDelete, path, nil, opts)
}

// GET issues a GET request against path (the full module-relative path,
// e.g. "/contacts" or "/contacts/01j...") as the harness's default user.
func (h *Harness) GET(path string, opts ...QueryOption) *Response {
	return h.baseState().GET(path, opts...)
}

// POST issues a POST request with body (JSON-encoded) against path.
func (h *Harness) POST(path string, body any, opts ...QueryOption) *Response {
	return h.baseState().POST(path, body, opts...)
}

// PUT issues a PUT request with body against path.
func (h *Harness) PUT(path string, body any, opts ...QueryOption) *Response {
	return h.baseState().PUT(path, body, opts...)
}

// PATCH issues a PATCH request with body against path.
func (h *Harness) PATCH(path string, body any, opts ...QueryOption) *Response {
	return h.baseState().PATCH(path, body, opts...)
}

// DELETE issues a DELETE request against path.
func (h *Harness) DELETE(path string, opts ...QueryOption) *Response {
	return h.baseState().DELETE(path, opts...)
}

func (s *requestState) do(method, path string, body any, opts []QueryOption) *Response {
	t := s.h.t
	t.Helper()

	u := &url.URL{Path: path}
	if len(opts) > 0 {
		q := u.Query()
		for _, o := range opts {
			q.Set(o.key, o.value)
		}
		u.RawQuery = q.Encode()
	}

	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("modeltest: marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, u.String(), bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	ctx := engine.WithTenantContext(req.Context(), &tenantresolve.TenantContext{
		TenantID: s.h.TenantID,
		Slug:     s.h.tenantSlug,
		Entitlements: tenantresolve.EntitlementSet{
			Features: map[string]bool{"module." + s.h.moduleName: true},
		},
	})

	authCtx := &authcheck.AuthContext{
		IsAuthenticated: !s.anonymous,
		UserID:          s.userID,
		TenantID:        s.h.TenantID,
		TenantSlug:      s.h.tenantSlug,
		Roles:           s.roles,
		RolesLive:       s.roles,
		PermissionSet:   s.perms,
	}
	if s.anonymous {
		authCtx.UserID = ""
		authCtx.PermissionSet = nil
	}
	ctx = engine.WithAuthContext(ctx, authCtx)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.h.handler.ServeHTTP(rec, req)

	return newResponse(t, rec)
}
