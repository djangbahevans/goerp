package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// runCLI runs Execute() with os.Args set to args (argv[0] plus the given
// arguments), capturing whatever it writes to os.Stdout/os.Stderr.
func runCLI(t *testing.T, args ...string) (exitCode int, stdout, stderr string) {
	t.Helper()

	origArgs := os.Args
	os.Args = append([]string{"goerp"}, args...)
	t.Cleanup(func() { os.Args = origArgs })

	origStdout, origStderr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	exitCode = Execute()

	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = origStdout, origStderr

	var outBuf, errBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, outR)
	_, _ = io.Copy(&errBuf, errR)

	return exitCode, outBuf.String(), errBuf.String()
}

func TestExecuteSuccess(t *testing.T) {
	dir := t.TempDir() + "/demo"

	code, stdout, stderr := runCLI(t, "module", "create", "demo", "--dir", dir)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "created module") {
		t.Errorf("stdout = %q, want it to mention the created module", stdout)
	}
}

func TestExecuteMissingArgIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "module", "create")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "Error:") {
		t.Errorf("stderr = %q, want an Error: line", stderr)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, want the command's usage synopsis for a usage error", stderr)
	}
}

func TestExecuteUnknownFlagIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "module", "create", "demo", "--bogus")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "Error:") {
		t.Errorf("stderr = %q, want an Error: line", stderr)
	}
}

func TestExecuteNonUsageErrorSkipsUsageDump(t *testing.T) {
	// Point --dir at a path with a regular file as a parent component, so
	// os.MkdirAll fails inside internalmodule.Create. This is a plain RunE
	// failure, not a usage mistake, so it must not print the usage block.
	blocker := t.TempDir() + "/not_a_dir"
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}

	code, _, stderr := runCLI(t, "module", "create", "demo", "--dir", blocker+"/demo")

	if code == 2 {
		t.Fatalf("exit code = 2, want a non-usage error code; stderr: %s", stderr)
	}
	if code == 0 {
		t.Fatalf("expected Create to fail when a path component is a regular file")
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, should not contain the usage dump for a non-usage error", stderr)
	}
}

func TestNewRootCmdListsAllSubcommandGroups(t *testing.T) {
	rootCmd := newRootCmd()

	got := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		got[c.Name()] = true
	}

	for _, want := range []string{"module", "events", "jobs", "schema", "tenant"} {
		if !got[want] {
			t.Errorf("root command is missing subcommand group %q", want)
		}
	}
}

func TestNewRootCmdRegistersGlobalFlags(t *testing.T) {
	rootCmd := newRootCmd()

	for _, name := range []string{
		"env", "admin-url", "admin-token", "client-cert", "client-key",
		"ca-cert", "timeout", "json", "quiet", "yes",
	} {
		if rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("root command is missing persistent flag --%s", name)
		}
	}
}
