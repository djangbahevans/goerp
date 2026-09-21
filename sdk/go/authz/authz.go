// Package authz is sdk/go's outbound module-side caller for the
// host.authz namespace (host-abi-reference.md §12) — currently just
// FieldCheck, calling host.authz.field_check via sdk/go/internal/hostcall.
package authz

import (
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// AccessKind selects which of a field's two FieldSecurityRule
// permissions FieldCheck evaluates.
type AccessKind = abi.AuthzFieldCheckKind

const (
	Read  = abi.AuthzFieldCheckRead
	Write = abi.AuthzFieldCheckWrite
)

type fieldCheckInput = abi.AuthzFieldCheckInput

type fieldCheckOutput = abi.AuthzFieldCheckOutput

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
