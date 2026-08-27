package wasm

import "testing"

func TestEvaluateFieldCheck_NoRuleDeclaredAllowsBoth(t *testing.T) {
	mc := newFieldSecModuleContext("authztest-norule")

	if !evaluateFieldCheck(mc, "testmodule.widget", "name", authzFieldCheckRead) {
		t.Error("expected an unrestricted field to allow read")
	}
	if !evaluateFieldCheck(mc, "testmodule.widget", "name", authzFieldCheckWrite) {
		t.Error("expected an unrestricted field to allow write")
	}
}

func TestEvaluateFieldCheck_GrantedVsDenied(t *testing.T) {
	granted := newFieldSecModuleContext("authztest-granted", "contacts:contact:financials_read")
	denied := newFieldSecModuleContext("authztest-denied")

	if !evaluateFieldCheck(granted, "testmodule.widget", "credit_limit", authzFieldCheckRead) {
		t.Error("expected the granted caller to be allowed to read credit_limit")
	}
	if evaluateFieldCheck(denied, "testmodule.widget", "credit_limit", authzFieldCheckRead) {
		t.Error("expected the caller without the permission to be denied read of credit_limit")
	}
}

func TestEvaluateFieldCheck_NilFieldSecRegistryAllows(t *testing.T) {
	mc := &ModuleContext{}
	if !evaluateFieldCheck(mc, "testmodule.widget", "credit_limit", authzFieldCheckRead) {
		t.Error("expected a nil FieldSecurityRegistry to allow rather than deny")
	}
}
