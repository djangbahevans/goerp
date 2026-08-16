package gateway

import "net/http"

// Must match internal/engine/adminapi/audit.go's own constant exactly.
const operatorIdentityHeader = "X-GoERP-Operator-Identity"

// identityMiddleware sets the identity header from the verified client
// cert's CN, deleting any client-supplied value first — without that, an
// authenticated caller could forge another operator's audit identity.
func identityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(operatorIdentityHeader)

		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			r.Header.Set(operatorIdentityHeader, operatorIdentity(r.TLS.PeerCertificates[0]))
		}

		next.ServeHTTP(w, r)
	})
}
