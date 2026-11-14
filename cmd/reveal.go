package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/prompt"
	"github.com/a-axa/wisanggeni/internal/reveal"
)

// DefaultMaxOutput caps the decompressed archive size on reveal (1 GiB, §5).
const DefaultMaxOutput int64 = 1 << 30

func newRevealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reveal <image>",
		Short: "Extract the hidden files from an image",
		Long: `Reveal decrypts the hidden blob and unpacks it into the output directory.
Archives are treated as untrusted: absolute paths, "..", symlinks and device
nodes are rejected.`,
		Args: cobra.ExactArgs(1),
		RunE: runReveal,
	}

	flags := cmd.Flags()
	flags.StringP("output", "o", ".", "destination directory")
	flags.Bool("force", false, "allow overwriting existing files")
	flags.Int64("max-output", DefaultMaxOutput, "maximum decompressed size in bytes")
	addPassphraseFlags(cmd)

	return cmd
}

// runReveal drives §7.9. Unlike hide there is nothing worth checking before the
// passphrase: the salt is public but the payload it keys is only readable with a
// key, so load, ask, then decrypt. The decryption itself lives in
// internal/reveal, which inspect and the TUI use too.
func runReveal(cmd *cobra.Command, args []string) error {
	imagePath := args[0]
	outDir := flagString(cmd, "output")
	force := flagBool(cmd, "force")
	maxOutput := flagInt64(cmd, "max-output")
	if maxOutput <= 0 {
		return fail("--max-output must be positive")
	}

	warn := notifier(cmd, false)

	car, err := carrier.Load(imagePath)
	if err != nil {
		return err
	}
	for _, msg := range car.Warnings {
		warn("%s", msg)
	}

	// Reveal needs the passphrase once; only hide confirms (§5).
	secret, err := prompt.Read(prompt.Options{
		File:      flagString(cmd, "passphrase-file"),
		FromStdin: flagBool(cmd, "passphrase-stdin"),
	})
	if err != nil {
		return err
	}
	defer clear(secret.Passphrase)
	for _, msg := range secret.Warnings {
		warn("%s", msg)
	}

	result, err := reveal.Open(car.Image, reveal.Options{
		Dir:        outDir,
		Force:      force,
		MaxOutput:  maxOutput,
		Passphrase: secret.Passphrase,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "revealed %d entries (%d bytes) into %s\n",
		len(result.Entries), result.Total, result.Dir)
	return nil
}
