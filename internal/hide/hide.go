// Package hide runs the embedding pipeline of §7.8.
//
// It is the single implementation behind `wisanggeni hide` and the TUI wizard,
// so the two surfaces cannot drift: the same checks in the same order, the same
// messages, the same exit codes. Nothing here reads a terminal or prints: the
// caller owns the passphrase and the output, which is what lets the TUI embed
// the same pipeline it drives with the keyboard.
//
// The job is split in two on purpose. Analyze does everything a failure can be
// caught by before a secret is needed — the output guard, the cover score, the
// pack, the fill policy — so an impossible job is refused without the user
// typing a passphrase. Execute is the part that needs one.
package hide

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// Stage names one step of the pipeline, so a caller can show progress without
// knowing the order of the work.
type Stage int

const (
	// StagePack builds the tar stream and compresses it.
	StagePack Stage = iota

	// StageDerive runs Argon2id and HKDF.
	StageDerive

	// StageSeal encrypts the header and the body.
	StageSeal

	// StageEmbed computes the cost map and runs STC.
	StageEmbed

	// StageSave writes the carrier image in the output format.
	StageSave

	// StageVerify reopens the output and checks the blob round-trips.
	StageVerify
)

// String returns the human label for a stage.
func (s Stage) String() string {
	switch s {
	case StagePack:
		return "Pack tar + zstd"
	case StageDerive:
		return "Argon2id + HKDF"
	case StageSeal:
		return "Seal container"
	case StageEmbed:
		return "STC embed"
	case StageSave:
		return "Save image"
	case StageVerify:
		return "Verify"
	default:
		return "unknown"
	}
}

// Options describes one hide job.
type Options struct {
	// Inputs are the files and folders to pack (§5.1).
	Inputs []string

	// CarrierPath is the image to embed into. It is never modified.
	CarrierPath string

	// OutputPath is where the stego image is written. Its extension picks the
	// output format: .bmp writes a BMP, anything else writes a PNG.
	OutputPath string

	// NoCompress skips zstd, for `hide --no-compress`.
	NoCompress bool

	// NoVerify skips the post-write verification.
	NoVerify bool

	// Force allows overwriting the output, a fill above 10% and a smooth cover.
	Force bool

	// Passphrase keys the container. Analyze ignores it; Execute needs it.
	Passphrase []byte

	// Salt pins the container salt. Nil means a fresh random salt. Tests set it
	// so that two runs of the same job produce the same bytes.
	Salt []byte

	// Nonces pins the two AEAD nonces. Nil means fresh random ones, which is what
	// every real run wants; tests set both (with Salt) so two writers can be
	// compared byte for byte (§6).
	Nonces *Nonces

	// Warn, when set, receives every advisory message as it is produced. Analyze
	// also records them on the plan, for callers that cannot print from the
	// goroutine the job runs on.
	Warn func(string)

	// Progress, when set, is called at the start of each stage.
	Progress func(Stage)
}

// Nonces is a pair of pinned AEAD nonces, used only by determinism tests.
// Production runs leave it nil and let crypto.Seal draw fresh ones.
type Nonces struct {
	Header []byte
	Body   []byte
}

// Plan is everything Analyze learns before a passphrase is needed.
type Plan struct {
	// CarrierPath and OutputPath echo the options.
	CarrierPath string
	OutputPath  string

	// Width and Height are the carrier dimensions.
	Width, Height int

	// Score is the §7.5 cover classification.
	Score stego.CoverStats

	// Capacity, Suggested and Strong are the §6.1 byte budgets.
	Capacity  int64
	Suggested int64
	Strong    int64

	// PayloadSize is len(body_plain); BlobSize adds the container overhead.
	PayloadSize int
	BlobSize    int64

	// Fill is the §6.1 fill ratio of BlobSize.
	Fill float64

	// Compressed reports whether the payload is zstd, i.e. the header flag bit.
	Compressed bool

	// Chunks are the types of the carrier chunks kept in the output. A BMP
	// carrier has none, so the list is empty for it.
	Chunks []string

	// Warnings are the advisory messages produced so far, in order.
	Warnings []string

	car    *carrier.Carrier
	packed archive.Packed
}

// Result reports a completed hide.
type Result struct {
	// Output is the path written.
	Output string

	// PayloadSize is len(body_plain); BlobSize includes the container overhead.
	PayloadSize int
	BlobSize    int64

	// Fill is the §6.1 fill ratio of BlobSize.
	Fill float64

	// Compressed reports whether the payload was zstd.
	Compressed bool

	// Verified reports whether the output was reopened and checked.
	Verified bool
}

// Analyze runs §7.8 steps 1-6: the output guard, the carrier, the cover score,
// the pack and the fill policy. It never asks for a passphrase and never writes.
//
// The returned plan is never nil, even on failure, so a caller that shows a
// report can still show whatever was learned — the warnings, the score, the
// capacity. Callers must test the error before trusting the rest.
func Analyze(opts Options) (*Plan, error) {
	if len(opts.Inputs) == 0 {
		return &Plan{}, exitcode.Fail(exitcode.Error, "hide: no input files")
	}
	if opts.CarrierPath == "" {
		return &Plan{}, exitcode.Fail(exitcode.Error, "hide: no carrier path")
	}
	if opts.OutputPath == "" {
		return &Plan{}, exitcode.Fail(exitcode.Error, "hide: no output path")
	}

	p := &Plan{CarrierPath: opts.CarrierPath, OutputPath: opts.OutputPath}
	warn := p.warner(opts.Warn)

	// Step 1: never write over the carrier, however the path is spelled, and
	// never replace an existing file unless forced.
	if err := checkOutput(opts.CarrierPath, opts.OutputPath, opts.Force); err != nil {
		return p, err
	}

	// Step 2.
	car, err := carrier.Load(opts.CarrierPath)
	if err != nil {
		return p, err
	}
	p.car = car
	p.Chunks = chunkTypes(car.Chunks)
	for _, msg := range car.Warnings {
		warn("%s", msg)
	}

	p.Width, p.Height = car.Image.Rect.Dx(), car.Image.Rect.Dy()
	p.Capacity = stego.Capacity(p.Width, p.Height)
	p.Suggested = stego.SuggestedCapacity(p.Width, p.Height)
	p.Strong = stego.StrongCapacity(p.Width, p.Height)

	// Step 3: refuse a carrier too smooth to hide in, unless forced.
	p.Score = stego.CoverScore(car.Image)
	if p.Score.Verdict == stego.CoverUnsuitable && !opts.Force {
		return p, exitcode.Fail(exitcode.Unfit,
			"carrier is too smooth to hide data (textured fraction %.3f, need %.2f); use --force to override",
			p.Score.TexturedFraction, stego.TexturedFractionWarn)
	}

	// Steps 4 and 5: pack under the usable capacity. Pack stops as soon as the
	// payload cannot fit, so it fails fast instead of building the whole stream.
	// A capacity of zero is passed as a one-byte budget: Pack reads zero as "no
	// limit", which would let an impossible job reach the embedder.
	p.stage(opts.Progress, StagePack)
	budget := p.Capacity
	if budget < 1 {
		budget = 1
	}
	packed, err := archive.Pack(opts.Inputs, archive.Options{
		MaxBytes:   budget,
		Exclude:    []string{opts.CarrierPath, opts.OutputPath},
		NoCompress: opts.NoCompress,
	})
	if err != nil {
		var tooLarge *archive.TooLargeError
		if errors.As(err, &tooLarge) {
			return p, exitcode.Fail(exitcode.Capacity,
				"capacity is %d bytes but the payload needs %d bytes uncompressed",
				p.Capacity, tooLarge.Uncompressed)
		}
		return p, err
	}
	p.packed = packed
	p.Compressed = packed.Compressed
	p.PayloadSize = len(packed.Data)

	// Step 6: fill policy. Nothing above 50% is allowed, force or not.
	p.BlobSize = int64(format.BlobOverhead + len(packed.Data))
	p.Fill = stego.FillRatio(p.BlobSize, p.Width, p.Height)
	switch classifyFill(p.Fill, opts.Force) {
	case fillReject:
		return p, exitcode.Fail(exitcode.Unfit,
			"fill is %.1f%%, above the %.0f%% limit even with --force",
			p.Fill*100, stego.FillReject*100)
	case fillNeedForce:
		return p, exitcode.Fail(exitcode.Unfit,
			"fill is %.1f%%, above %.0f%%; use --force to accept",
			p.Fill*100, stego.FillForce*100)
	case fillWarn:
		warn("fill is %.1f%%, above %.0f%%; the payload is easier to detect",
			p.Fill*100, stego.FillWarn*100)
	case fillOK:
	}
	if p.Score.Verdict == stego.CoverMarginal {
		warn("carrier is only marginally textured (%.3f)", p.Score.TexturedFraction)
	}

	return p, nil
}

// Execute runs §7.8 steps 7-15 against a plan: key derivation, the container,
// the embed and the write. It is the only part that needs the passphrase.
func Execute(p *Plan, opts Options) (Result, error) {
	if p == nil || p.car == nil {
		return Result{}, exitcode.Fail(exitcode.Error, "hide: no analyzed plan")
	}

	salt, err := saltFor(opts.Salt)
	if err != nil {
		return Result{}, err
	}

	p.stage(opts.Progress, StageDerive)
	keys, err := crypto.Derive(string(opts.Passphrase), salt)
	if err != nil {
		return Result{}, err
	}

	p.stage(opts.Progress, StageSeal)
	var headerFlags uint8
	if p.packed.Compressed {
		headerFlags = format.FlagZstd
	}
	var blob []byte
	if opts.Nonces != nil {
		blob, err = crypto.SealWithNonces(keys.EncKey, salt, headerFlags, p.packed.Data,
			opts.Nonces.Header, opts.Nonces.Body)
	} else {
		blob, err = crypto.Seal(keys.EncKey, salt, headerFlags, p.packed.Data)
	}
	if err != nil {
		return Result{}, err
	}

	// Steps 10-12: cost map, the salt at S, the rest through STC over P'.
	p.stage(opts.Progress, StageEmbed)
	if err := stego.EmbedBlob(p.car.Image, blob, keys.PosSeed, stego.CostMap(p.car.Image)); err != nil {
		return Result{}, err
	}

	// Steps 13-15: write a temporary file, verify it, rename it into place.
	verify := !opts.NoVerify
	if err := p.writeAtomic(blob, keys.PosSeed, verify, opts.Progress); err != nil {
		return Result{}, err
	}

	return Result{
		Output:      p.OutputPath,
		PayloadSize: p.PayloadSize,
		BlobSize:    p.BlobSize,
		Fill:        p.Fill,
		Compressed:  p.Compressed,
		Verified:    verify,
	}, nil
}

// Run is Analyze followed by Execute, for callers that have the passphrase up
// front.
func Run(opts Options) (Result, error) {
	plan, err := Analyze(opts)
	if err != nil {
		return Result{}, err
	}
	return Execute(plan, opts)
}

// stage reports a pipeline step to the caller's callback, if there is one.
func (p *Plan) stage(progress func(Stage), s Stage) {
	if progress != nil {
		progress(s)
	}
}

// warner returns the closure that records and forwards advisory messages.
func (p *Plan) warner(sink func(string)) func(string, ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		p.Warnings = append(p.Warnings, msg)
		if sink != nil {
			sink(msg)
		}
	}
}

// saltFor returns the container salt: the pinned one when given, a fresh random
// one otherwise.
func saltFor(pinned []byte) ([]byte, error) {
	if pinned == nil {
		salt := make([]byte, format.SaltSize)
		if _, err := cryptorand.Read(salt); err != nil {
			return nil, fmt.Errorf("generate salt: %w", err)
		}
		return salt, nil
	}
	if len(pinned) != format.SaltSize {
		return nil, exitcode.Fail(exitcode.Error,
			"hide: pinned salt is %d bytes, want %d", len(pinned), format.SaltSize)
	}
	return append([]byte(nil), pinned...), nil
}

// chunkTypes lists the carrier chunk types kept in the output, for a report.
func chunkTypes(chunks []carrier.Chunk) []string {
	if len(chunks) == 0 {
		return nil
	}
	types := make([]string, len(chunks))
	for i, c := range chunks {
		types[i] = c.Type
	}
	return types
}

// fillDecision is the §5 fill policy applied to a fill ratio.
type fillDecision int

const (
	// fillOK is at or below the warning threshold.
	fillOK fillDecision = iota

	// fillWarn is above FillWarn but still allowed.
	fillWarn

	// fillNeedForce is above FillForce and requires force.
	fillNeedForce

	// fillReject is above FillReject and is refused even with force.
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
// carrier (even with force), and may only replace an existing file when forced.
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
		return exitcode.Fail(exitcode.Error,
			"output %q is the carrier; refusing even with --force", outputPath)
	}
	if !force {
		return exitcode.Fail(exitcode.Error,
			"output %q already exists; use --force to overwrite", outputPath)
	}
	return nil
}

// writeAtomic writes the image to a temporary file next to the destination,
// optionally verifies it, and renames it into place. A failure anywhere removes
// the temporary file, so a broken run never leaves a half-written output.
func (p *Plan) writeAtomic(blob []byte, posSeed [32]byte, verify bool, progress func(Stage)) error {
	dir := filepath.Dir(p.OutputPath)
	// The temporary file keeps the output's extension, because carrier.Save
	// chooses the encoder from the file name. A .bmp output written to a .png
	// temporary would be encoded as PNG and verified as the wrong format.
	pattern := ".wisanggeni-*"
	if ext := filepath.Ext(p.OutputPath); ext != "" {
		pattern += ext
	}
	tmp, err := os.CreateTemp(dir, pattern)
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

	p.stage(progress, StageSave)
	if err := carrier.Save(tmpPath, p.car.Image, p.car.Chunks); err != nil {
		cleanup()
		return err
	}
	// CreateTemp uses 0600; the result is an image the user will share.
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("set output permissions: %w", err)
	}

	if verify {
		p.stage(progress, StageVerify)
		if err := verifyOutput(tmpPath, blob, posSeed); err != nil {
			cleanup()
			return err
		}
	}

	if err := os.Rename(tmpPath, p.OutputPath); err != nil {
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
		return exitcode.Fail(exitcode.Verify, "verification could not reopen the output: %v", err)
	}
	if err := stego.VerifyBlob(car.Image, blob, posSeed); err != nil {
		return exitcode.Fail(exitcode.Verify, "post-write verification failed: %v", err)
	}
	return nil
}
