package cmd

import "github.com/spf13/cobra"

// DefaultMaxOutput caps the decompressed archive size on reveal (1 GiB, §5).
const DefaultMaxOutput int64 = 1 << 30

func newRevealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reveal <image.png>",
		Short: "Extract the hidden files from a PNG",
		Long: `Reveal decrypts the hidden blob and unpacks it into the output directory.
Archives are treated as untrusted: absolute paths, "..", symlinks and device
nodes are rejected.`,
		Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			return notImplemented("reveal")
		},
	}

	flags := cmd.Flags()
	flags.StringP("output", "o", ".", "destination directory")
	flags.Bool("force", false, "allow overwriting existing files")
	flags.Int64("max-output", DefaultMaxOutput, "maximum decompressed size in bytes")
	addPassphraseFlags(cmd)

	return cmd
}
