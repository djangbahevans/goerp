// Package authz is sdk/go's outbound module-side caller for the
// host.authz namespace (host-abi-reference.md §12) — Check, Require,
// RowFilter, UserRoles and FieldCheck, calling host.authz.check, require,
// row_filter, user_roles and field_check via sdk/go/internal/hostcall.
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

// WhereFragment is a SQL condition and the values of its $1.. placeholders.
// SQL is empty or begins with "AND", so it can follow an existing WHERE
// condition.
type WhereFragment struct {
	SQL    string
	Params []any
}

// RowFilter returns the condition the ABAC policies on tableName amount to
// for the request's user and permission p: "AND FALSE" when the user lacks p,
// an empty fragment when no policy restricts p on the table, and an
// "AND (...)" clause over the table's columns otherwise.
//
// It is an introspection primitive, for example to explain why a record is
// not visible. Row-level security already filters every query, so appending
// the fragment to a module's own query filters the same rows twice.
func RowFilter(tableName string, p perm.Permission) (WhereFragment, error) {
	if p.Name() == "" {
		return WhereFragment{}, errZeroPermission
	}
	var out abi.AuthzRowFilterOutput
	err := hostcall.Do(hostAuthzRowFilter, abi.AuthzRowFilterInput{
		TableName:  tableName,
		Permission: p.Name(),
	}, &out)
	return WhereFragment{SQL: out.SQL, Params: out.Params}, err
}

// Role is a role the request's user holds in the tenant. IsSystem marks a
// built-in role, one a tenant cannot edit or delete.
type Role struct {
	ID       string
	Name     string
	IsSystem bool
}

// UserRoles returns the roles the request's user currently holds in the
// tenant, ordered by name; empty when the user holds none. Role names are
// chosen by each tenant, so use it to display or branch on a role, and use
// Check to decide whether the user may do something.
func UserRoles() ([]Role, error) {
	var out abi.AuthzUserRolesOutput
	if err := hostcall.Do(hostAuthzUserRoles, abi.AuthzUserRolesInput{}, &out); err != nil {
		return nil, err
	}
	roles := make([]Role, len(out.Roles))
	for i, r := range out.Roles {
		roles[i] = Role{ID: r.ID, Name: r.Name, IsSystem: r.IsSystem}
	}
	return roles, nil
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
