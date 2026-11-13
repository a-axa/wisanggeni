package cmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/tui"
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
// It only runs when stdout is a terminal: every useful thing it can do is also
// available as a subcommand, so a redirected stdout gets a clear error instead
// of an interactive program writing escape codes into a pipe.
func runTUI(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	if !isTerminal(out) {
		return fail(ExitError, "stdout is not a terminal; use the subcommands instead")
	}
	// The worker goroutines behind a hide/reveal job cannot touch the Model
	// directly, so they post stage updates through tea.Program.Send. The model is
	// built before the program exists, hence the indirection: the closure captures
	// the variable that is assigned on the next line.
	var program *tea.Program
	model := tui.New(cmd.Root().Version).WithSender(func(msg tea.Msg) {
		if program != nil {
			program.Send(msg)
		}
	})
	program = tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithOutput(out),
	)
	if _, err := program.Run(); err != nil {
		return fail(ExitError, "TUI failed: %v", err)
	}
	return nil
}
