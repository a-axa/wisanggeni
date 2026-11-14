package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/inspect"
	"github.com/a-axa/wisanggeni/internal/stego"
)

func newScoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "score <carrier>",
		Short: "Classify a carrier as a cover",
		Long: `Score reports textured_fraction, median_rho and the verdict of §7.5.
It needs no passphrase and never writes anything.

A "reject" verdict is a report, not a failure: the exit code stays 0 so the
command composes in scripts.`,
		Args: cobra.ExactArgs(1),
		RunE: runScore,
	}
}

func runScore(cmd *cobra.Command, args []string) error {
	// Open rather than Load, so a transparent carrier still gets scored.
	car, err := carrier.Open(args[0])
	if err != nil {
		return err
	}
	warn := notifier(cmd, false)
	for _, msg := range car.Warnings {
		warn("%s", msg)
	}

	width, height := car.Image.Rect.Dx(), car.Image.Rect.Dy()
	// Score always: a verdict is still the most useful thing to say about a
	// transparent carrier, even though the engine will refuse to embed in it.
	info := inspect.Describe(car, true)

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "file                %s\n", args[0])
	_, _ = fmt.Fprintf(out, "dimensions          %dx%d\n", width, height)
	_, _ = fmt.Fprintf(out, "textured fraction   %.3f (%.2f is the suitable threshold)\n",
		info.Score.TexturedFraction, stego.TexturedFractionGood)
	_, _ = fmt.Fprintf(out, "median rho          %.6g\n", info.Score.MedianRho)
	_, _ = fmt.Fprintf(out, "verdict             %s\n", info.Score.Verdict)
	if car.Alpha {
		_, _ = fmt.Fprintf(out, "note                the carrier has transparency and cannot be used\n")
	}
	return nil
}
