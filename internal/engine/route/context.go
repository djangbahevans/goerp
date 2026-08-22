package route

import "context"

type paramsContextKey struct{}

// WithParams returns a context carrying params — the path parameters
// RouteTable.Lookup extracted for the current request (e.g. {id} in
// "/admin/users/{id}/mfa/reset") — so a builtin route handler can read
// them back via ParamsFromContext without re-parsing the URL itself.
func WithParams(ctx context.Context, params map[string]string) context.Context {
	return context.WithValue(ctx, paramsContextKey{}, params)
}

// ParamsFromContext returns the path parameters WithParams stored on ctx,
// or an empty map if none were stored.
func ParamsFromContext(ctx context.Context) map[string]string {
	params, _ := ctx.Value(paramsContextKey{}).(map[string]string)
	if params == nil {
		return map[string]string{}
	}
	return params
}
