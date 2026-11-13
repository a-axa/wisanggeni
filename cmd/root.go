// Package cmd is the command-line surface: one file per subcommand plus the
// shared flag, exit-code and error plumbing. It holds no engine logic; every
// operation is delegated to internal/ so the TUI can reuse it unchanged.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/a-axa/wisanggeni/internal/exitcode"
)

// Exit codes are part of the CLI contract (§5, "Perilaku umum"). The numbers
// live in internal/exitcode so the engine packages behind the CLI and the TUI can
// tag their own failures; these aliases keep the command layer's names.
const (
	ExitOK       = exitcode.OK
	ExitError    = exitcode.Error
	ExitCapacity = exitcode.Capacity
	ExitDecrypt  = exitcode.Decrypt
	ExitVerify   = exitcode.Verify
	ExitUnfit    = exitcode.Unfit
)

// fail builds an error that carries a documented exit code.
func fail(code int, format string, args ...any) error {
	return exitcode.Fail(code, format, args...)
}

// Execute runs the command tree and returns the process exit code. Errors are
// printed here, once, so no command has to worry about formatting.
func Execute(version string) int {
	err := newRootCmd(version).Execute()
	if err == nil {
		return ExitOK
	}
	fmt.Fprintf(os.Stderr, "wisanggeni: %v\n", err)

	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return ExitError
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "wisanggeni",
		Short:         "Hide files and folders inside PNG images",
		Long:          rootLong,
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd)
		},
	}

	// The six documented subcommands are the whole surface; cobra's generated
	// completion command is noise for this tool.
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("wisanggeni {{.Version}}\n")
	root.SetOut(os.Stdout)

	root.AddCommand(
		newHideCmd(),
		newRevealCmd(),
		newCapacityCmd(),
		newScoreCmd(),
		newInspectCmd(),
		newTUICmd(),
	)
	return root
}

const rootLong = `Wisanggeni compresses, encrypts and hides files inside a PNG image.

Every channel of the output changes by at most ±1 and the result keeps the
carrier dimensions. The blob has no magic bytes and no plaintext length: with
the wrong passphrase there is nothing to tell it apart from noise.

This is not undetectable. Read the threat model in the README before relying
on it for anything that matters.

Run without a subcommand to open the TUI.`

// addPassphraseFlags installs the three mutually exclusive ways of supplying a
// passphrase. The passphrase never travels through argv or the environment.
func addPassphraseFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.String("passphrase-file", "", "read the passphrase from this file")
	flags.Bool("passphrase-stdin", false, "read one line of passphrase from stdin (no confirmation)")
	cmd.MarkFlagsMutuallyExclusive("passphrase-file", "passphrase-stdin")
}

// flagString reads a string flag that the command itself declared, so the error
// pflag would return for an unknown flag cannot happen here.
func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// flagBool reads a bool flag that the command itself declared.
func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

// flagInt64 reads an int64 flag that the command itself declared.
func flagInt64(cmd *cobra.Command, name string) int64 {
	v, _ := cmd.Flags().GetInt64(name)
	return v
}

// notifier returns a warning printer that respects --quiet. Warnings go to
// stderr so they never contaminate a redirected stdout.
func notifier(cmd *cobra.Command, quiet bool) func(string, ...any) {
	if quiet {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wisanggeni: "+format+"\n", args...)
	}
}

// isTerminal reports whether w is a terminal, which the TUI requires (§5.2).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
