package cmd

import "github.com/spf13/cobra"

func newHideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hide <input...>",
		Short: "Hide files or folders inside a PNG carrier",
		Long: `Hide packs the inputs (§5.1), encrypts the result and embeds it into the
carrier image. The carrier is never modified; output goes to a new PNG.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(*cobra.Command, []string) error {
			return notImplemented("hide")
		},
	}

	flags := cmd.Flags()
	flags.StringP("carrier", "c", "", "carrier PNG image (required)")
	flags.StringP("output", "o", "", "output PNG path (required)")
	flags.Bool("no-compress", false, "skip zstd compression")
	flags.Bool("no-verify", false, "skip post-write verification (not recommended)")
	flags.Bool("force", false, "allow overwriting output, fill > 10%, and smooth covers")
	flags.BoolP("quiet", "q", false, "suppress non-error output")
	addPassphraseFlags(cmd)

	return cmd
}
