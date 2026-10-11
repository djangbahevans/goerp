package orm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestRegisterConstraint_BindsModelAndPhase(t *testing.T) {
	previous := constraintRegistry
	constraintRegistry = map[constraintKey]ConstraintFunc{}
	t.Cleanup(func() { constraintRegistry = previous })

	calls := 0
	RegisterConstraint[testModel](OnDelete, func(_ ConstraintContext, _ map[string]any) *ConstraintResult {
		calls++

		return Reject("state", "cannot delete")
	})

	for _, req := range []abi.ConstraintRequest{
		{Model: "other.model", Phase: "delete"},
		{Model: "test.model", Phase: "write"},
	} {
		resp := dispatchConstraintAndDecode(t, req)
		if resp.Error != nil || !resp.Allowed {
			t.Fatalf("unregistered request %+v: %+v", req, resp)
		}
	}

	if calls != 0 {
		t.Fatalf("hook ran %d times for another model or phase", calls)
	}

	resp := dispatchConstraintAndDecode(t, abi.ConstraintRequest{Model: "test.model", Phase: "delete"})
	if resp.Error != nil || resp.Allowed || resp.Field != "state" || calls != 1 {
		t.Fatalf("registered request: response %+v, calls %d", resp, calls)
	}
}

func TestRegisterPreviewHook_BindsModel(t *testing.T) {
	previous := previewRegistry
	previewRegistry = map[string]PreviewHook{}
	t.Cleanup(func() { previewRegistry = previous })

	calls := 0
	RegisterPreviewHook[testModel](func(_ PreviewContext, draft map[string]any) map[string]any {
		calls++
		draft["computed"] = true

		return draft
	})

	other := dispatchPreviewAndDecode(t, abi.PreviewRequest{
		Model:  "other.model",
		Record: map[string]any{"id": "other"},
	})
	if other.Error != nil || other.Record["id"] != "other" || other.Record["computed"] != nil || calls != 0 {
		t.Fatalf("another model: response %+v, calls %d", other, calls)
	}

	resp := dispatchPreviewAndDecode(t, abi.PreviewRequest{Model: "test.model", Record: map[string]any{}})
	if resp.Error != nil || resp.Record["computed"] != true || calls != 1 {
		t.Fatalf("registered model: response %+v, calls %d", resp, calls)
	}
}

func TestRegisterVirtualBackend_BindsModel(t *testing.T) {
	previous := registry
	registry = map[string]VirtualBackend{}
	t.Cleanup(func() { registry = previous })

	calls := 0
	RegisterVirtualBackend[testModel](VirtualBackend{
		Read: func(_ VirtualContext, id string) (map[string]any, error) {
			calls++

			return map[string]any{"id": id}, nil
		},
	})

	other := dispatchAndDecode(t, abi.VirtualOpRequest{Model: "other.model", Op: "read", ID: "other"})
	if other.Error == nil || other.Error.Code != "orm.virtual_op_not_implemented" || calls != 0 {
		t.Fatalf("another model: response %+v, calls %d", other, calls)
	}

	resp := dispatchAndDecode(t, abi.VirtualOpRequest{Model: "test.model", Op: "read", ID: "registered"})
	if resp.Error != nil || resp.Record["id"] != "registered" || calls != 1 {
		t.Fatalf("registered model: response %+v, calls %d", resp, calls)
	}
}

func TestRegistrations_RejectInvalidModelArguments(t *testing.T) {
	registrations := []struct {
		name      string
		arguments string
	}{
		{name: "RegisterConstraint", arguments: "orm.OnCreate, nil"},
		{name: "RegisterPreviewHook", arguments: "nil"},
		{name: "RegisterVirtualBackend", arguments: "orm.VirtualBackend{}"},
	}

	for _, registration := range registrations {
		for _, test := range []struct {
			name string
			call string
			want string
		}{
			{
				name: "non_model",
				call: fmt.Sprintf("orm.%s[int](%s)", registration.name, registration.arguments),
				want: "int does not satisfy",
			},
			{
				name: "misspelled_model",
				call: fmt.Sprintf("orm.%s[MissingModel](%s)", registration.name, registration.arguments),
				want: "undefined: MissingModel",
			},
			{
				name: "string_model",
				call: fmt.Sprintf("orm.%s(\"test.model\", %s)", registration.name, registration.arguments),
				want: "too many arguments in call to orm." + registration.name,
			},
		} {
			t.Run(registration.name+"/"+test.name, func(t *testing.T) {
				dir := t.TempDir()
				file := filepath.Join(dir, "main.go")
				source := fmt.Sprintf(`package main
import "github.com/djangbahevans/goerp/sdk/go/orm"
func main() { %s }
`, test.call)
				if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}

				cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "invalid"), file)
				output, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(output), test.want) {
					t.Fatalf("invalid registration build: %v, want %q\n%s", err, test.want, output)
				}
			})
		}
	}
}
