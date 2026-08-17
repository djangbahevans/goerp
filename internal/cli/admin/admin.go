package admin

import (
	"github.com/djangbahevans/goerp/internal/cli/admin/operators"
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Engine administration commands",
	}

	cmd.AddCommand(operators.NewCmd())

	return cmd
}
