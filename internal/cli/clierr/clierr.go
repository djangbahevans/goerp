package clierr

import "github.com/spf13/cobra"

type ExitCoder interface {
	error
	ExitCode() int
}

type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string {
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	return e.Err
}

func (e *Error) ExitCode() int {
	return e.Code
}

func Usage(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: 2, Err: err}
}

func WrapArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return Usage(fn(cmd, args))
	}
}
