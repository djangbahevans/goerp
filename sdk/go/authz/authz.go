// Package authz is sdk/go's outbound module-side caller for the
// host.authz namespace (host-abi-reference.md §12) — currently just
// FieldCheck, calling host.authz.field_check via sdk/go/internal/hostcall.
package authz

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// AccessKind selects which of a field's two FieldSecurityRule
// permissions FieldCheck evaluates.
type AccessKind int

const (
	Read AccessKind = iota
	Write
)

type fieldCheckInput struct {
	UserID string     `msgpack:"user_id"`
	Model  string     `msgpack:"model"`
	Field  string     `msgpack:"field"`
	Kind   AccessKind `msgpack:"kind"`
}

type fieldCheckOutput struct {
	Allowed bool `msgpack:"allowed"`
}

// FieldCheck reports whether userID — the calling module's own request
// user — may access modelName.fieldName per the field's declared
// .Access() rule (a field with no declared rule always returns true).
// modelName is the qualified "{module}.{model}" name, not a table name.
func FieldCheck(userID, modelName, fieldName string, kind AccessKind) (bool, error) {
	var out fieldCheckOutput
	err := hostcall.Do(hostAuthzFieldCheck, fieldCheckInput{
		UserID: userID,
		Model:  modelName,
		Field:  fieldName,
		Kind:   kind,
	}, &out)
	return out.Allowed, err
}
