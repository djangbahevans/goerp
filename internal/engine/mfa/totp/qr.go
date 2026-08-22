package totp

import (
	"bytes"
	"errors"
	"fmt"

	qrcode "github.com/yeqown/go-qrcode/v2"
)

// qrModuleSize is the pixel size of one QR module in the rendered SVG —
// arbitrary but large enough to stay reliably scannable at typical
// display sizes; the viewBox keeps it responsive regardless.
const qrModuleSize = 8

// svgWriter implements qrcode.Writer, rendering a Matrix's bitmap directly
// as SVG <rect> elements. Used instead of any of this library's own
// image-format writers so the QR code never round-trips through a raster
// format — auth-internals.md §8 requires an SVG specifically.
type svgWriter struct {
	buf bytes.Buffer
}

func (w *svgWriter) Write(mat qrcode.Matrix) error {
	bitmap := mat.Bitmap()
	height := len(bitmap)
	if height == 0 {
		return errors.New("empty qr matrix")
	}
	width := len(bitmap[0])

	fmt.Fprintf(&w.buf, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`,
		width*qrModuleSize, height*qrModuleSize)
	w.buf.WriteString(`<rect width="100%" height="100%" fill="#fff"/>`)
	for y, row := range bitmap {
		for x, set := range row {
			if !set {
				continue
			}
			fmt.Fprintf(&w.buf, `<rect x="%d" y="%d" width="%d" height="%d" fill="#000"/>`,
				x*qrModuleSize, y*qrModuleSize, qrModuleSize, qrModuleSize)
		}
	}
	w.buf.WriteString(`</svg>`)
	return nil
}

func (w *svgWriter) Close() error { return nil }

// qrCodeSVG renders uri as a server-generated SVG QR code. The caller
// passes the full otpauth:// URL — this function has no knowledge of the
// secret it embeds, and nothing here logs uri.
func qrCodeSVG(uri string) ([]byte, error) {
	qr, err := qrcode.New(uri)
	if err != nil {
		return nil, fmt.Errorf("encode qr matrix: %w", err)
	}

	w := &svgWriter{}
	if err := qr.Save(w); err != nil {
		return nil, fmt.Errorf("render qr svg: %w", err)
	}
	return w.buf.Bytes(), nil
}
