package engine

import "testing"

func TestRouter_HandleDecodesEscapedPathSegments(t *testing.T) {
	r := withRouter(t)
	var gotEmail string
	// Registered first: the SDK router takes the first matching route.
	GET("/by-email/export", func(*Request) *Response { return &Response{StatusCode: 204} })
	GET("/by-email/{email}", func(req *Request) *Response {
		gotEmail = req.PathParams["email"]
		return &Response{StatusCode: 200}
	})

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
