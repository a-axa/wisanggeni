package cmd

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/prompt"
	"github.com/a-axa/wisanggeni/internal/stego"
)

func newHideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hide <input...>",
		Short: "Hide files or folders inside a PNG carrier",
		Long: `Hide packs the inputs (§5.1), encrypts the result and embeds it into the
carrier image. The carrier is never modified; output goes to a new PNG.

The carrier is scored and the payload is measured before the passphrase is
asked for, so an impossible job fails without making you type it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: runHide,
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

// runHide drives §7.8 in order. Everything up to the passphrase prompt is free
// or cheap, so a capacity, fill or cover problem is reported before the user is
// asked for a secret.
func runHide(cmd *cobra.Command, inputs []string) error {
	carrierPath := flagString(cmd, "carrier")
	outputPath := flagString(cmd, "output")
	force := flagBool(cmd, "force")
	quiet := flagBool(cmd, "quiet")

	if carrierPath == "" {
		return fail(ExitError, "--carrier is required")
	}
	if outputPath == "" {
		return fail(ExitError, "--output is required")
	}

	// Step 1: never write over the carrier, however the path is spelled, and
	// never replace an existing file unless forced.
	if err := checkOutput(carrierPath, outputPath, force); err != nil {
		return err
	}

	// Step 2.
	car, err := carrier.Load(carrierPath)
	if err != nil {
		return err
	}
	warn := notifier(cmd, quiet)
	for _, msg := range car.Warnings {
		warn("%s", msg)
	}

	width, height := car.Image.Rect.Dx(), car.Image.Rect.Dy()

	// Step 3: refuse a carrier too smooth to hide in, unless forced.
	score := stego.CoverScore(car.Image)
	if score.Verdict == stego.CoverUnsuitable && !force {
		return fail(ExitUnfit,
			"carrier is too smooth to hide data (textured fraction %.3f, need %.2f); use --force to override",
			score.TexturedFraction, stego.TexturedFractionWarn)
	}

	// Steps 4 and 5: pack under the usable capacity. Pack stops as soon as the
	// payload cannot fit, so it fails fast instead of building the whole stream.
	// A capacity of zero is passed as a one-byte budget: Pack reads zero as "no
	// limit", which would let an impossible job reach the embedder.
	capacity := stego.Capacity(width, height)
	budget := capacity
	if budget < 1 {
		budget = 1
	}
	packed, err := archive.Pack(inputs, archive.Options{
		MaxBytes:   budget,
		Exclude:    []string{carrierPath, outputPath},
		NoCompress: flagBool(cmd, "no-compress"),
	})
	if err != nil {
		var tooLarge *archive.TooLargeError
		if errors.As(err, &tooLarge) {
			return fail(ExitCapacity,
				"capacity is %d bytes but the payload needs %d bytes uncompressed",
				capacity, tooLarge.Uncompressed)
		}
		return err
	}

	// Step 6: fill policy. Nothing above 50% is allowed, force or not.
	blobSize := int64(format.BlobOverhead + len(packed.Data))
	fill := stego.FillRatio(blobSize, width, height)
	switch classifyFill(fill, force) {
	case fillReject:
		return fail(ExitUnfit, "fill is %.1f%%, above the %.0f%% limit even with --force",
			fill*100, stego.FillReject*100)
	case fillNeedForce:
		return fail(ExitUnfit, "fill is %.1f%%, above %.0f%%; use --force to accept",
			fill*100, stego.FillForce*100)
	case fillWarn:
		warn("fill is %.1f%%, above %.0f%%; the payload is easier to detect",
			fill*100, stego.FillWarn*100)
	case fillOK:
	}
	if score.Verdict == stego.CoverMarginal {
		warn("carrier is only marginally textured (%.3f)", score.TexturedFraction)
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

	// Step 8: fresh random salt, then the keys.
	salt := make([]byte, format.SaltSize)
	if _, err := cryptorand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	keys, err := crypto.Derive(string(secret.Passphrase), salt)
	if err != nil {
		return err
	}

	// Step 9.
	var headerFlags uint8
	if packed.Compressed {
		headerFlags = format.FlagZstd
	}
	blob, err := crypto.Seal(keys.EncKey, salt, headerFlags, packed.Data)
	if err != nil {
		return err
	}

	// Steps 10-12: cost map, salt at S, the rest through STC over P'.
	costs := stego.CostMap(car.Image)
	if err := stego.EmbedBlob(car.Image, blob, keys.PosSeed, costs); err != nil {
		return err
	}

	// Steps 13-15: write a temporary file, verify it, rename it into place.
	if err := writeAtomic(outputPath, car, blob, keys.PosSeed, !flagBool(cmd, "no-verify")); err != nil {
		return err
	}

	if !quiet {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes payload, fill %.1f%%)\n",
			outputPath, len(packed.Data), fill*100)
	}
	return nil
}

// fillDecision is the §5 fill policy applied to a fill ratio.
type fillDecision int

const (
	// fillOK is at or below the warning threshold.
	fillOK fillDecision = iota

	// fillWarn is above FillWarn but still allowed.
	fillWarn

	// fillNeedForce is above FillForce and requires --force.
	fillNeedForce

	// fillReject is above FillReject and is refused even with --force.
	fillReject
)

// classifyFill applies the fill thresholds of §5.
func classifyFill(fill float64, force bool) fillDecision {
	switch {
	case fill > stego.FillReject:
		return fillReject
	case fill > stego.FillForce && !force:
		return fillNeedForce
	case fill > stego.FillWarn:
		return fillWarn
	default:
		return fillOK
	}
}

// checkOutput enforces the two output rules of §5: the output may never be the
// carrier (even with --force), and may only replace an existing file when
// forced.
func checkOutput(carrierPath, outputPath string, force bool) error {
	outInfo, err := os.Stat(outputPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect output %q: %w", outputPath, err)
	}

	carrierInfo, err := os.Stat(carrierPath)
	if err != nil {
		return fmt.Errorf("inspect carrier %q: %w", carrierPath, err)
	}
	if os.SameFile(carrierInfo, outInfo) {
		return fail(ExitError, "output %q is the carrier; refusing even with --force", outputPath)
	}
	if !force {
		return fail(ExitError, "output %q already exists; use --force to overwrite", outputPath)
	}
	return nil
}

// writeAtomic writes the image to a temporary file next to the destination,
// optionally verifies it, and renames it into place. A failure anywhere removes
// the temporary file, so a broken run never leaves a half-written output.
func writeAtomic(outputPath string, car *carrier.Carrier, blob []byte, posSeed [32]byte, verify bool) error {
	dir := filepath.Dir(outputPath)
	tmp, err := os.CreateTemp(dir, ".wisanggeni-*.png")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tmpPath := tmp.Name()
	// carrier.Save creates the file itself; the descriptor is just for naming.
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary output: %w", err)
	}
	cleanup := func() { _ = os.Remove(tmpPath) }

	if err := carrier.Save(tmpPath, car.Image, car.Chunks); err != nil {
		cleanup()
		return err
	}
	// CreateTemp uses 0600; the result is an image the user will share.
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("set output permissions: %w", err)
	}

	if verify {
		if err := verifyOutput(tmpPath, blob, posSeed); err != nil {
			cleanup()
			return err
		}
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		cleanup()
		return fmt.Errorf("move output into place: %w", err)
	}
	return nil
}

// verifyOutput reopens the written file through the normal load path and checks
// that the embedded blob round-trips exactly (§7.8 step 14). No key derivation
// happens here: posSeed is already in hand.
func verifyOutput(path string, blob []byte, posSeed [32]byte) error {
	car, err := carrier.Load(path)
	if err != nil {
		return fail(ExitVerify, "verification could not reopen the output: %v", err)
	}
	if err := stego.VerifyBlob(car.Image, blob, posSeed); err != nil {
		return fail(ExitVerify, "post-write verification failed: %v", err)
	}
	return nil
}
