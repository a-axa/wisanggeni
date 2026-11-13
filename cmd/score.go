package cmd

import "github.com/spf13/cobra"

func newScoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "score <carrier.png>",
		Short: "Classify a carrier as a cover",
		Long: `Score reports textured_fraction, median_rho and the verdict of §7.5.
It needs no passphrase and never writes anything.`,
		Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			return notImplemented("score")
		},
	}
}
