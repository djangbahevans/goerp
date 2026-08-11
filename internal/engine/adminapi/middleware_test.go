package adminapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAdminAuthMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		authHeader string
		want       int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized},
		{"empty bearer against non-empty token", "Bearer ", http.StatusUnauthorized},
		{"correct token", "Bearer correct-token", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handler := adminAuthMiddleware("correct-token")(okHandler())

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != c.want {
				t.Errorf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}
