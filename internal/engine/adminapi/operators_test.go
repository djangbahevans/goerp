package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/operatorcert"
	"github.com/djangbahevans/goerp/internal/engine/vaultpki"
)

type fakeOperatorPKI struct {
	issued        *vaultpki.IssuedCert
	issuedCN      string
	issuedTTL     time.Duration
	revokedSerial string
}

func (f *fakeOperatorPKI) IssueCert(ctx context.Context, cn string, ttl time.Duration) (*vaultpki.IssuedCert, error) {
	f.issuedCN = cn
	f.issuedTTL = ttl
	return f.issued, nil
}

func (f *fakeOperatorPKI) RevokeCert(ctx context.Context, serial string) error {
	f.revokedSerial = serial
	return nil
}

type fakeOperatorLedger struct {
	serials map[string]string
	revoked []string
}

func newFakeOperatorLedger() *fakeOperatorLedger {
	return &fakeOperatorLedger{serials: map[string]string{}}
}

func (f *fakeOperatorLedger) RecordIssuance(ctx context.Context, name, serial string, expiresAt time.Time) error {
	f.serials[name] = serial
	return nil
}

func (f *fakeOperatorLedger) SerialForName(ctx context.Context, name string) (string, error) {
	serial, ok := f.serials[name]
	if !ok {
		return "", operatorcert.ErrCertificateNotFound
	}
	return serial, nil
}

func (f *fakeOperatorLedger) MarkRevoked(ctx context.Context, name string) error {
	f.revoked = append(f.revoked, name)
	delete(f.serials, name)
	return nil
}

func TestIssueCertRoute_NoPKIReturnsNotImplemented(t *testing.T) {
	mux := http.NewServeMux()
	RegisterOperatorsRoutes(mux, OperatorsDeps{})

	req := httptest.NewRequest(http.MethodPost, "/admin/operators/issue-cert", strings.NewReader(`{"name":"kwame"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestIssueCertRoute_MissingNameIsBadRequest(t *testing.T) {
	mux := http.NewServeMux()
	RegisterOperatorsRoutes(mux, OperatorsDeps{PKI: &fakeOperatorPKI{}, Ledger: newFakeOperatorLedger()})

	req := httptest.NewRequest(http.MethodPost, "/admin/operators/issue-cert", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIssueCertRoute_Success(t *testing.T) {
	pki := &fakeOperatorPKI{issued: &vaultpki.IssuedCert{
		CertificatePEM: "cert-pem",
		PrivateKeyPEM:  "key-pem",
		SerialNumber:   "11:22:33",
	}}
	ledger := newFakeOperatorLedger()
	mux := http.NewServeMux()
	RegisterOperatorsRoutes(mux, OperatorsDeps{PKI: pki, Ledger: ledger})

	req := httptest.NewRequest(http.MethodPost, "/admin/operators/issue-cert", strings.NewReader(`{"name":"kwame","expires":"30d"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	if pki.issuedCN != "kwame" {
		t.Errorf("issued CN = %q, want %q", pki.issuedCN, "kwame")
	}
	if pki.issuedTTL != 30*24*time.Hour {
		t.Errorf("issued TTL = %v, want %v", pki.issuedTTL, 30*24*time.Hour)
	}
	if ledger.serials["kwame"] != "11:22:33" {
		t.Errorf("ledger serial for kwame = %q, want %q", ledger.serials["kwame"], "11:22:33")
	}

	var env struct {
		Data struct {
			Certificate  string `json:"certificate"`
			PrivateKey   string `json:"private_key"`
			SerialNumber string `json:"serial_number"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Certificate != "cert-pem" || env.Data.PrivateKey != "key-pem" || env.Data.SerialNumber != "11:22:33" {
		t.Errorf("response data = %+v, missing expected fields", env.Data)
	}
}

func TestIssueCertRoute_DefaultExpiryIs90Days(t *testing.T) {
	pki := &fakeOperatorPKI{issued: &vaultpki.IssuedCert{CertificatePEM: "c", PrivateKeyPEM: "k", SerialNumber: "s"}}
	mux := http.NewServeMux()
	RegisterOperatorsRoutes(mux, OperatorsDeps{PKI: pki, Ledger: newFakeOperatorLedger()})

	req := httptest.NewRequest(http.MethodPost, "/admin/operators/issue-cert", strings.NewReader(`{"name":"kwame"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if pki.issuedTTL != defaultCertTTL {
		t.Errorf("issued TTL = %v, want default %v", pki.issuedTTL, defaultCertTTL)
	}
}

func TestRevokeCertRoute_Success(t *testing.T) {
	pki := &fakeOperatorPKI{}
	ledger := newFakeOperatorLedger()
	ledger.serials["kwame"] = "11:22:33"
	mux := http.NewServeMux()
	RegisterOperatorsRoutes(mux, OperatorsDeps{PKI: pki, Ledger: ledger})

	req := httptest.NewRequest(http.MethodPost, "/admin/operators/revoke-cert", strings.NewReader(`{"name":"kwame"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if pki.revokedSerial != "11:22:33" {
		t.Errorf("revoked serial = %q, want %q", pki.revokedSerial, "11:22:33")
	}
	if len(ledger.revoked) != 1 || ledger.revoked[0] != "kwame" {
		t.Errorf("ledger revoked = %v, want [kwame]", ledger.revoked)
	}
}

func TestRevokeCertRoute_UnknownNameReturnsNotFound(t *testing.T) {
	mux := http.NewServeMux()
	RegisterOperatorsRoutes(mux, OperatorsDeps{PKI: &fakeOperatorPKI{}, Ledger: newFakeOperatorLedger()})

	req := httptest.NewRequest(http.MethodPost, "/admin/operators/revoke-cert", strings.NewReader(`{"name":"does-not-exist"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestParseExpiry(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"90d", 90 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"2160h", 2160 * time.Hour, false},
		{"not-a-duration", 0, true},
	}
	for _, c := range cases {
		got, err := parseExpiry(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseExpiry(%q) error = nil, want an error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseExpiry(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseExpiry(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
