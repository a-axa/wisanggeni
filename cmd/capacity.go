package cmd

import "github.com/spf13/cobra"

func newCapacityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capacity <carrier.png>",
		Short: "Report how much a carrier can hold",
		Long: `Capacity prints the carrier dimensions and all four capacity numbers
(§6.1), the cover score, and whether the carrier has alpha or colour chunks.`,
		Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			return notImplemented("capacity")
		},
	}
}
