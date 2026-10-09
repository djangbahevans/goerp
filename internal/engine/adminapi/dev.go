package adminapi

import (
	"context"
	"net/http"
)

type DevSession struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type DevBootstrapper interface {
	BootstrapDev(context.Context, string) (DevSession, error)
}

func RegisterDevRoute(mux *http.ServeMux, enabled bool, environment, domain string, bootstrapper DevBootstrapper) {
	if !enabled || environment != "development" || domain != "localhost" {
		return
	}

	mux.HandleFunc("POST /admin/dev/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeJSON[struct {
			Module string `json:"module"`
		}](r)
		if err != nil || req.Module == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "module is required")
			return
		}

		result, err := bootstrapper.BootstrapDev(r.Context(), req.Module)
		if err != nil {
			writeError(w, http.StatusConflict, "dev_bootstrap_failed", err.Error())
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeData(w, http.StatusOK, result)
	})
}
