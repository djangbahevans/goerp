package adminapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/operatorcert"
	"github.com/djangbahevans/goerp/internal/engine/vaultpki"
)

// OperatorPKI is satisfied by *vaultpki.Client — issues/revokes short-lived
// operator mTLS client certificates for the admin gateway (goerp#179).
type OperatorPKI interface {
	IssueCert(ctx context.Context, cn string, ttl time.Duration) (*vaultpki.IssuedCert, error)
	RevokeCert(ctx context.Context, serial string) error
}

// OperatorCertLedger is satisfied by *operatorcert.Store — tracks which
// serial number a named operator's live certificate was issued under,
// since Vault PKI revokes by serial but `revoke-cert` takes a name
// (cli-reference.md §10).
type OperatorCertLedger interface {
	RecordIssuance(ctx context.Context, name, serial string, expiresAt time.Time) error
	SerialForName(ctx context.Context, name string) (string, error)
	MarkRevoked(ctx context.Context, name string) error
}

type OperatorsDeps struct {
	PKI    OperatorPKI
	Ledger OperatorCertLedger
}

func RegisterOperatorsRoutes(mux *http.ServeMux, deps OperatorsDeps) {
	h := &operatorsHandlers{deps: deps}
	mux.HandleFunc("POST /admin/operators/issue-cert", h.issueCert)
	mux.HandleFunc("POST /admin/operators/revoke-cert", h.revokeCert)
}

type operatorsHandlers struct {
	deps OperatorsDeps
}

const defaultCertTTL = 90 * 24 * time.Hour

var dayDurationPattern = regexp.MustCompile(`^(\d+)d$`)

// parseDayDuration accepts a Go duration string ("2160h") or a bare day
// count ("90d"/"30d") — several cli-reference.md flags (`--expires`,
// `--grace-period`) default to an "Nd" value, a unit Go's own
// time.ParseDuration doesn't support. Shared across every admin handler
// that takes one of these flags, not specific to certificate expiry.
func parseDayDuration(s string) (time.Duration, error) {
	if m := dayDurationPattern.FindStringSubmatch(s); m != nil {
		days, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

type issueCertRequest struct {
	Name    string `json:"name"`
	Expires string `json:"expires"`
}

func (h *operatorsHandlers) issueCert(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[issueCertRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}

	ttl := defaultCertTTL
	if req.Expires != "" {
		parsed, err := parseDayDuration(req.Expires)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("invalid expires value: %s", err.Error()))
			return
		}
		ttl = parsed
	}

	if h.deps.PKI == nil {
		writeNotImplemented(w, "goerp#179")
		return
	}

	issued, err := h.deps.PKI.IssueCert(r.Context(), req.Name, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Recorded for revoke-cert's name -> serial lookup only — the
	// certificate and private key themselves are returned below and never
	// persisted (this handler's own AC).
	if h.deps.Ledger != nil {
		expiresAt := time.Now().Add(ttl)
		if err := h.deps.Ledger.RecordIssuance(r.Context(), req.Name, issued.SerialNumber, expiresAt); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}

	writeData(w, http.StatusOK, struct {
		Certificate  string `json:"certificate"`
		PrivateKey   string `json:"private_key"`
		SerialNumber string `json:"serial_number"`
	}{Certificate: issued.CertificatePEM, PrivateKey: issued.PrivateKeyPEM, SerialNumber: issued.SerialNumber})
}

type revokeCertRequest struct {
	Name string `json:"name"`
}

func (h *operatorsHandlers) revokeCert(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[revokeCertRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}

	if h.deps.PKI == nil || h.deps.Ledger == nil {
		writeNotImplemented(w, "goerp#179")
		return
	}

	serial, err := h.deps.Ledger.SerialForName(r.Context(), req.Name)
	if err != nil {
		if errors.Is(err, operatorcert.ErrCertificateNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no live certificate found for that name")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	if err := h.deps.PKI.RevokeCert(r.Context(), serial); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	if err := h.deps.Ledger.MarkRevoked(r.Context(), req.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusOK, struct {
		Name         string `json:"name"`
		SerialNumber string `json:"serial_number"`
		Status       string `json:"status"`
	}{Name: req.Name, SerialNumber: serial, Status: "revoked"})
}
