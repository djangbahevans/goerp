package module

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

// newTestCmd wraps `go test` against the current directory's Go tests —
// the module test harness (sdk/go/modeltest) does the actual work of
// compiling the module and dispatching against it from inside those
// tests, so this command's own job is just translating its double-dash
// flags to go test's flags and streaming its output live, the same way
// a developer running `go test` directly would see it.
func newTestCmd() *cobra.Command {
	var run, tags string
	var verbose, cover, race bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "test [test-pattern]",
		Short: "Run a module's Go tests against the real engine test harness",
		Args:  clierr.WrapArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A bare positional is shorthand for --run <pattern>
			// (cli-reference.md's own synopsis, "[test-pattern]") —
			// the only pattern-filtering behavior any doc actually
			// describes, so a positional and --run mean the same
			// thing; the positional wins if both are given.
			pattern := run
			if len(args) > 0 {
				pattern = args[0]
			}

			goArgs := []string{"test"}
			if pattern != "" {
				goArgs = append(goArgs, "-run", pattern)
			}
			if verbose {
				goArgs = append(goArgs, "-v")
			}
			if cover {
				goArgs = append(goArgs, "-cover")
			}
			if race {
				goArgs = append(goArgs, "-race")
			}
			if tags != "" {
				goArgs = append(goArgs, "-tags", tags)
			}
			goArgs = append(goArgs, "-timeout", timeout.String(), "./...")

			goTest := exec.CommandContext(cmd.Context(), "go", goArgs...)
			goTest.Stdout = cmd.OutOrStdout()
			goTest.Stderr = cmd.ErrOrStderr()

			if err := goTest.Run(); err != nil {
				return fmt.Errorf("run module tests: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&run, "run", "", "Run only tests matching this pattern (translated to go test -run)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show test output (translated to go test -v)")
	cmd.Flags().BoolVar(&cover, "cover", false, "Generate coverage report (translated to go test -cover)")
	cmd.Flags().BoolVar(&race, "race", false, "Enable race detector (translated to go test -race)")
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated build tags to include, e.g. integration (translated to go test -tags)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Test timeout (translated to go test -timeout)")

	return cmd
}
