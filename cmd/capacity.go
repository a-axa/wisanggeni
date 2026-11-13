package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/stego"
)

func newCapacityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capacity <carrier.png>",
		Short: "Report how much a carrier can hold",
		Long: `Capacity prints the carrier dimensions and all four capacity numbers
(§6.1), the cover score, and whether the carrier has alpha or colour chunks.

Nothing is written and no passphrase is needed.`,
		Args: cobra.ExactArgs(1),
		RunE: runCapacity,
	}
}

func runCapacity(cmd *cobra.Command, args []string) error {
	car, err := openCarrierForReport(cmd, args[0])
	if err != nil {
		return err
	}
	printCarrier(cmd.OutOrStdout(), args[0], car)
	return nil
}

// openCarrierForReport opens a carrier for the reporting commands, which tolerate
// alpha so that a transparent carrier still gets a report that says why it cannot
// be used. Engine warnings go to stderr.
func openCarrierForReport(cmd *cobra.Command, path string) (*carrier.Carrier, error) {
	car, err := carrier.Open(path)
	if err != nil {
		return nil, err
	}
	warn := notifier(cmd, false)
	for _, msg := range car.Warnings {
		warn("%s", msg)
	}
	return car, nil
}

// printCarrier writes the carrier layer, the part both capacity and inspect can
// report without a passphrase (§7.10).
func printCarrier(out io.Writer, path string, car *carrier.Carrier) {
	width, height := car.Image.Rect.Dx(), car.Image.Rect.Dy()

	_, _ = fmt.Fprintf(out, "file          %s\n", path)
	_, _ = fmt.Fprintf(out, "dimensions    %dx%d\n", width, height)
	_, _ = fmt.Fprintf(out, "channels      %d\n", stego.Channels(width, height))
	_, _ = fmt.Fprintf(out, "capacity raw  %d bytes\n", stego.CapacityRaw(width, height))
	_, _ = fmt.Fprintf(out, "usable        %d bytes\n", stego.Capacity(width, height))
	_, _ = fmt.Fprintf(out, "suggested     %d bytes (10%% fill)\n", stego.SuggestedCapacity(width, height))
	_, _ = fmt.Fprintf(out, "strong        %d bytes (5%% fill)\n", stego.StrongCapacity(width, height))

	if car.Alpha {
		_, _ = fmt.Fprintf(out, "cover         not scored\n")
		_, _ = fmt.Fprintf(out, "alpha         yes; transparent carriers are not supported, so nothing can be hidden\n")
	} else {
		stats := stego.CoverScore(car.Image)
		_, _ = fmt.Fprintf(out, "cover         %s (textured_fraction %.3f, median_rho %.6g)\n",
			stats.Verdict, stats.TexturedFraction, stats.MedianRho)
		_, _ = fmt.Fprintf(out, "alpha         no\n")
	}

	_, _ = fmt.Fprintf(out, "chunks        %s\n", chunkList(car.Chunks))
}

// chunkList names the carrier's preserved chunks in file order.
func chunkList(chunks []carrier.Chunk) string {
	if len(chunks) == 0 {
		return "none"
	}
	types := make([]string, len(chunks))
	for i, c := range chunks {
		types[i] = c.Type
	}
	return strings.Join(types, ", ")
}
