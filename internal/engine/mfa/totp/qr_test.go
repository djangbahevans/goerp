package totp

import (
	"strings"
	"testing"
)

func TestQRCodeSVG_ProducesWellFormedSVG(t *testing.T) {
	svg, err := qrCodeSVG("otpauth://totp/GoERP:user@example.com?secret=JBSWY3DPEHPK3PXP&issuer=GoERP")
	if err != nil {
		t.Fatalf("qrCodeSVG() error: %v", err)
	}

	s := string(svg)
	if !strings.HasPrefix(s, "<svg ") {
		t.Errorf("output doesn't start with <svg : %q", s[:min(40, len(s))])
	}
	if !strings.HasSuffix(s, "</svg>") {
		t.Error("output doesn't end with </svg>")
	}
	if !strings.Contains(s, "<rect") {
		t.Error("output has no <rect> elements — expected at least the background and some QR modules")
	}
}

func TestQRCodeSVG_NeverEmbedsTheRawURI(t *testing.T) {
	// The rendered SVG is a bitmap of black/white squares — it must never
	// contain the input text itself (e.g. as an SVG comment or metadata),
	// since that URI carries the TOTP secret in plaintext.
	uri := "otpauth://totp/GoERP:user@example.com?secret=JBSWY3DPEHPK3PXP&issuer=GoERP"
	svg, err := qrCodeSVG(uri)
	if err != nil {
		t.Fatalf("qrCodeSVG() error: %v", err)
	}

	if strings.Contains(string(svg), "JBSWY3DPEHPK3PXP") {
		t.Error("rendered SVG contains the raw secret text — must be an image only")
	}
}
