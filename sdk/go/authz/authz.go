// Package authz is sdk/go's outbound module-side caller for the
// host.authz namespace (host-abi-reference.md §12) — Check, Require and
// FieldCheck, calling host.authz.check, require and field_check via
// sdk/go/internal/hostcall.
package authz

import (
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/djangbahevans/goerp/sdk/go/perm"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// ForbiddenError is Require's error for a caller who lacks the permission.
// It wraps the host's authz.forbidden error, so errors.As reaches the
// *abi.HostError too.
type ForbiddenError struct {
	Permission string
	Err        error
}

func (e *ForbiddenError) Error() string { return e.Err.Error() }
func (e *ForbiddenError) Unwrap() error { return e.Err }

var errZeroPermission = errors.New("authz: zero perm.Permission")

// Check reports whether the request's user holds p. A non-empty resourceID
// also requires the record's ABAC policies for p to admit it: policies
// combine with OR, so one admitting policy is enough, and a permission no
// policy scopes is a pure role check. A denial returns false with a nil
// error; a resourceID naming no record is an authz.resource_not_found error.
func Check(p perm.Permission, resourceID string) (bool, error) {
	if p.Name() == "" {
		return false, errZeroPermission
	}
	var out abi.AuthzCheckOutput
	err := hostcall.Do(hostAuthzCheck, abi.AuthzCheckInput{
		Permission: p.Name(),
		ResourceID: resourceID,
	}, &out)
	return out.Allowed, err
}

// Require is Check that returns a *ForbiddenError when the request's user
// lacks p, for a handler to pass to engine.FromHostError. resourceID scopes the
// check to a record's ABAC policies; see Check.
func Require(p perm.Permission, resourceID string) error {
	if p.Name() == "" {
		return errZeroPermission
	}
	err := hostcall.Do(hostAuthzRequire, abi.AuthzCheckInput{
		Permission: p.Name(),
		ResourceID: resourceID,
	}, nil)
	return forbiddenOrErr(p, err)
}

func forbiddenOrErr(p perm.Permission, err error) error {
	if hostErr, ok := errors.AsType[*abi.HostError](err); ok && hostErr.Code == abi.ErrCodeAuthzForbidden {
		return &ForbiddenError{Permission: p.Name(), Err: fmt.Errorf("require %s: %w", p.Name(), err)}
	}
	return err
}

// AccessKind selects which of a field's two FieldSecurityRule
// permissions FieldCheck evaluates.
type AccessKind = abi.AuthzFieldCheckKind

const (
	Read  = abi.AuthzFieldCheckRead
	Write = abi.AuthzFieldCheckWrite
)

// FieldCheck reports whether the request's user may access
// modelName.fieldName per the field's declared .Access() rule (a field with
// no declared rule always returns true). modelName is the qualified
// "{module}.{model}" name, not a table name.
func FieldCheck(modelName, fieldName string, kind AccessKind) (bool, error) {
	var out abi.AuthzFieldCheckOutput
	err := hostcall.Do(hostAuthzFieldCheck, abi.AuthzFieldCheckInput{
		Model: modelName,
		Field: fieldName,
		Kind:  kind,
	}, &out)
	return out.Allowed, err
}
