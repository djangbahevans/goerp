package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const moduleTestFixtureGoMod = `module goerp-cli-test-fixture

go 1.27.0
`

const moduleTestFixturePassingTest = `package fixture

import "testing"

func TestPasses(t *testing.T) {}
`

const moduleTestFixtureFailingTest = `package fixture

import "testing"

func TestFails(t *testing.T) {
	t.Fatal("boom")
}

func TestOtherPasses(t *testing.T) {}
`

func writeModuleTestFixture(t *testing.T, dir, testFile string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(moduleTestFixtureGoMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte(testFile), 0o644); err != nil {
		t.Fatalf("write fixture_test.go: %v", err)
	}
}

func TestModuleTest_TooManyArgsIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "module", "test", "one", "two")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, want the command's usage synopsis for a usage error", stderr)
	}
}

func TestModuleTest_PassingSuiteExitsZero(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixturePassingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
}

func TestModuleTest_FailingSuiteExitsNonZeroWithReadableSummary(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixtureFailingTest)
	t.Chdir(dir)

	code, stdout, _ := runCLI(t, "module", "test")

	if code == 0 {
		t.Fatal("expected a non-zero exit code for a failing test suite")
	}
	if code == 2 {
		t.Fatal("a test failure is not a CLI usage error and should not exit 2")
	}
	if !strings.Contains(stdout, "TestFails") || !strings.Contains(stdout, "boom") {
		t.Errorf("stdout = %q, want a readable summary naming the failing test", stdout)
	}
}

func TestModuleTest_RunFlagFiltersToMatchingTest(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixtureFailingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test", "--run", "TestOtherPasses")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — --run should have excluded the failing test (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
}

func TestModuleTest_PositionalPatternFiltersLikeRunFlag(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixtureFailingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test", "TestOtherPasses")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — the positional pattern should have excluded the failing test (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
}

func TestModuleTest_PositionalPatternTakesPrecedenceOverRunFlag(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixtureFailingTest)
	t.Chdir(dir)

	// --run points at the failing test; the positional pattern points at
	// a passing one — the documented precedence is that the positional
	// wins, so this should pass rather than fail.
	code, stdout, stderr := runCLI(t, "module", "test", "TestOtherPasses", "--run", "TestFails")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — positional pattern should take precedence over --run (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
}

func TestModuleTest_CoverFlagShowsCoverageSummary(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixturePassingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test", "--cover")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "coverage:") {
		t.Errorf("stdout = %q, want --cover to show a coverage summary", stdout)
	}
}

func TestModuleTest_TimeoutFlagAppliesGoTestTimeout(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixturePassingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test", "--verbose", "--timeout", "1m")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "TestPasses") {
		t.Errorf("stdout = %q, want the suite to have actually run under the given timeout", stdout)
	}
}

func TestModuleTest_RaceFlagStillPasses(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixturePassingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test", "--race")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — --race shouldn't itself fail a race-free suite (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
}

func TestModuleTest_VerboseFlagShowsPerTestOutput(t *testing.T) {
	dir := t.TempDir()
	writeModuleTestFixture(t, dir, moduleTestFixturePassingTest)
	t.Chdir(dir)

	code, stdout, stderr := runCLI(t, "module", "test", "--verbose")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "TestPasses") {
		t.Errorf("stdout = %q, want --verbose to show the individual test name", stdout)
	}
}
