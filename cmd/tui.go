package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive TUI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd)
		},
	}
}

// runTUI is the single entry point for both `wisanggeni` and `wisanggeni tui`.
// The Bubble Tea model arrives in F1; until then this fails loudly so the
// missing feature is never mistaken for a successful no-op.
func runTUI(cmd *cobra.Command) error {
	if !isTerminal(cmd.OutOrStdout()) {
		return fail(ExitError, "stdout is not a terminal; use the subcommands instead")
	}
	return errors.New("TUI is not implemented yet")
}
