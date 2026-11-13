package cmd

import "github.com/spf13/cobra"

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <image.png>",
		Short: "Show carrier and payload information",
		Long: `Inspect shows carrier information without a passphrase. With a passphrase
it also decrypts the payload. Note that the full mode costs as much as reveal:
it pays Argon2, decrypts header and body, decompresses, and parses the archive
in memory without writing anything to disk.`,
		Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			return notImplemented("inspect")
		},
	}

	cmd.Flags().Bool("header-only", false, "decrypt only the header (version, flags, body_len)")
	addPassphraseFlags(cmd)

	return cmd
}
