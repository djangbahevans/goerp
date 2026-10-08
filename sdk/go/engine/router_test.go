package engine

import "testing"

func TestRouter_HandleDecodesEscapedPathSegments(t *testing.T) {
	r := withRouter(t)
	var gotEmail string
	GET("/by-email/{email}", func(req *Request) *Response {
		gotEmail = req.PathParams["email"]
		return &Response{StatusCode: 200}
	})
	GET("/by-email/export", func(*Request) *Response { return &Response{StatusCode: 204} })

	tests := []struct {
		name, path string
		params     map[string]string
		wantStatus int
		wantEmail  string
	}{
		{
			name:       "encoded slash stays in one parameter",
			path:       "/by-email/a%2Bb%2Fc%40d.test",
			wantStatus: 200,
			wantEmail:  "a+b/c@d.test",
		},
		{
			name:       "engine-supplied decoded params are not overwritten with escaped values",
			path:       "/by-email/a%2Fb",
			params:     map[string]string{"email": "a/b"},
			wantStatus: 200,
			wantEmail:  "a/b",
		},
		{name: "encoded literal segment matches", path: "/by-email/%65xport", wantStatus: 204},
		{name: "unencoded slash is a separator", path: "/by-email/a/b", wantStatus: 404},
		{name: "malformed escape", path: "/by-email/%zz", wantStatus: 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotEmail = ""
			resp := r.Handle(&Request{Method: "GET", Path: tc.path, PathParams: tc.params})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if gotEmail != tc.wantEmail {
				t.Errorf("email = %q, want %q", gotEmail, tc.wantEmail)
			}
		})
	}
}

func TestRouter_HandlePrefersStaticOverParameterSegment(t *testing.T) {
	registerShow := func() {
		GET("/{id}", func(req *Request) *Response {
			return &Response{StatusCode: 200, Headers: map[string]string{"X-ID": req.PathParams["id"]}}
		})
	}
	registerExport := func() {
		GET("/export", func(*Request) *Response { return &Response{StatusCode: 204} })
	}

	orders := map[string][]func(){
		"parameter first": {registerShow, registerExport},
		"static first":    {registerExport, registerShow},
	}
	for name, register := range orders {
		t.Run(name, func(t *testing.T) {
			r := withRouter(t)
			for _, f := range register {
				f()
			}

			if got := r.Handle(&Request{Method: "GET", Path: "/export"}).StatusCode; got != 204 {
				t.Errorf("GET /export status = %d, want 204", got)
			}
			resp := r.Handle(&Request{Method: "GET", Path: "/abc"})
			if resp.StatusCode != 200 || resp.Headers["X-ID"] != "abc" {
				t.Errorf("GET /abc = %d id %q, want 200 id abc", resp.StatusCode, resp.Headers["X-ID"])
			}
			if got := r.Handle(&Request{Method: "GET", Path: "/%7Bid%7D"}).Headers["X-ID"]; got != "{id}" {
				t.Errorf("literal braces id = %q, want {id}", got)
			}
		})
	}
}

func TestRouter_HandleDoesNotBacktrackFromStaticSegment(t *testing.T) {
	r := withRouter(t)
	GET("/{id}/detail", func(*Request) *Response { return &Response{StatusCode: 200} })
	GET("/export/list", func(*Request) *Response { return &Response{StatusCode: 204} })

	if got := r.Handle(&Request{Method: "GET", Path: "/export/detail"}).StatusCode; got != 404 {
		t.Errorf("status = %d, want 404 (the engine's route table does not backtrack)", got)
	}
	if got := r.Handle(&Request{Method: "GET", Path: "/abc/detail"}).StatusCode; got != 200 {
		t.Errorf("status = %d, want 200", got)
	}
}

func TestRouter_HandleStaticPrecedenceIgnoresMethod(t *testing.T) {
	r := withRouter(t)
	GET("/{id}", func(*Request) *Response { return &Response{StatusCode: 200} })
	POST("/export", func(*Request) *Response { return &Response{StatusCode: 204} })

	if got := r.Handle(&Request{Method: "GET", Path: "/export"}).StatusCode; got != 404 {
		t.Errorf("GET /export status = %d, want 404 (the engine answers 405 for it)", got)
	}
	if got := r.Handle(&Request{Method: "POST", Path: "/export"}).StatusCode; got != 204 {
		t.Errorf("POST /export status = %d, want 204", got)
	}
}
