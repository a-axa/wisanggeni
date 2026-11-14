package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/hide"
	"github.com/a-axa/wisanggeni/internal/prompt"
)

func newHideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hide <input...>",
		Short: "Hide files or folders inside an image carrier",
		Long: `Hide packs the inputs (§5.1), encrypts the result and embeds it into the
carrier image. The carrier is never modified; output goes to a new image.

The carrier and the output may be PNG or BMP. The format is sniffed from the
file's content, not from its name, and the output format follows the output
extension (.bmp writes a BMP, anything else writes a PNG).

The carrier is scored and the payload is measured before the passphrase is
asked for, so an impossible job fails without making you type it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: runHide,
	}

	flags := cmd.Flags()
	flags.StringP("carrier", "c", "", "carrier image, PNG or BMP (required)")
	flags.StringP("output", "o", "", "output image path; .bmp for BMP, else PNG (required)")
	flags.Bool("no-compress", false, "skip zstd compression")
	flags.Bool("no-verify", false, "skip post-write verification (not recommended)")
	flags.Bool("force", false, "allow overwriting output, fill > 10%, and smooth covers")
	flags.BoolP("quiet", "q", false, "suppress non-error output")
	addPassphraseFlags(cmd)

	return cmd
}

// runHide reads the flags and hands the job to internal/hide, which the TUI calls
// too. This file only decides where the passphrase comes from and what gets
// printed; the checks, their order and their messages all live in the engine.
func runHide(cmd *cobra.Command, inputs []string) error {
	carrierPath := flagString(cmd, "carrier")
	outputPath := flagString(cmd, "output")
	quiet := flagBool(cmd, "quiet")

	if carrierPath == "" {
		return fail("--carrier is required")
	}
	if outputPath == "" {
		return fail("--output is required")
	}

	warn := notifier(cmd, quiet)
	opts := hide.Options{
		Inputs:      inputs,
		CarrierPath: carrierPath,
		OutputPath:  outputPath,
		NoCompress:  flagBool(cmd, "no-compress"),
		NoVerify:    flagBool(cmd, "no-verify"),
		Force:       flagBool(cmd, "force"),
		Warn:        func(msg string) { warn("%s", msg) },
	}

	// Steps 1-6. Everything up to the passphrase is free or cheap, so a capacity,
	// fill or cover problem is reported before the user is asked for a secret.
	plan, err := hide.Analyze(opts)
	if err != nil {
		return err
	}

	// Step 7: only now is the passphrase needed.
	secret, err := prompt.Read(prompt.Options{
		File:      flagString(cmd, "passphrase-file"),
		FromStdin: flagBool(cmd, "passphrase-stdin"),
		Confirm:   true,
	})
	if err != nil {
		return err
	}
	defer clear(secret.Passphrase)
	for _, msg := range secret.Warnings {
		warn("%s", msg)
	}
	opts.Passphrase = secret.Passphrase

	result, err := hide.Execute(plan, opts)
	if err != nil {
		return err
	}

	if !quiet {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes payload, fill %.1f%%)\n",
			result.Output, result.PayloadSize, result.Fill*100)
	}
	return nil
}
