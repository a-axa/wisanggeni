package cmd

import (
	"errors"
	"fmt"
	"image"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/prompt"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// DefaultMaxOutput caps the decompressed archive size on reveal (1 GiB, §5).
const DefaultMaxOutput int64 = 1 << 30

// payloadPrefix is the part of the payload a reader must have before it can
// decrypt the header: header_nonce | header_ct | body_nonce (§6).
const payloadPrefix = format.BodyCipherOffset - format.SaltSize

func newRevealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reveal <image.png>",
		Short: "Extract the hidden files from a PNG",
		Long: `Reveal decrypts the hidden blob and unpacks it into the output directory.
Archives are treated as untrusted: absolute paths, "..", symlinks and device
nodes are rejected.`,
		Args: cobra.ExactArgs(1),
		RunE: runReveal,
	}

	flags := cmd.Flags()
	flags.StringP("output", "o", ".", "destination directory")
	flags.Bool("force", false, "allow overwriting existing files")
	flags.Int64("max-output", DefaultMaxOutput, "maximum decompressed size in bytes")
	addPassphraseFlags(cmd)

	return cmd
}

// runReveal drives §7.9. Unlike hide there is nothing worth checking before the
// passphrase: the salt is public but the payload it keys is only readable with a
// key, so load, ask, then decrypt.
func runReveal(cmd *cobra.Command, args []string) error {
	imagePath := args[0]
	outDir := flagString(cmd, "output")
	force := flagBool(cmd, "force")
	maxOutput := flagInt64(cmd, "max-output")
	if maxOutput <= 0 {
		return fail(ExitError, "--max-output must be positive")
	}

	warn := notifier(cmd, false)

	car, err := carrier.Load(imagePath)
	if err != nil {
		return err
	}
	for _, msg := range car.Warnings {
		warn("%s", msg)
	}

	// Reveal needs the passphrase once; only hide confirms (§5).
	secret, err := prompt.Read(prompt.Options{
		File:      flagString(cmd, "passphrase-file"),
		FromStdin: flagBool(cmd, "passphrase-stdin"),
	})
	if err != nil {
		return err
	}
	defer clear(secret.Passphrase)
	for _, msg := range secret.Warnings {
		warn("%s", msg)
	}

	unpacked, err := revealInto(car.Image, secret.Passphrase, outDir, force, maxOutput)
	if err != nil {
		return err
	}

	var total int64
	for _, entry := range unpacked.Entries {
		total += entry.Size
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "revealed %d entries (%d bytes) into %s\n",
		len(unpacked.Entries), total, outDir)
	return nil
}

// revealInto runs §7.9 against an already decoded carrier.
func revealInto(img *image.NRGBA, passphrase []byte, dir string, force bool, maxOutput int64) (archive.Unpacked, error) {
	// Step 3: the salt sits at public positions, so it is readable without a key.
	salt, err := stego.ExtractSalt(img, format.SaltSize)
	if err != nil {
		return archive.Unpacked{}, errReveal()
	}
	keys, err := crypto.Derive(string(passphrase), salt)
	if err != nil {
		return archive.Unpacked{}, err
	}

	// Step 5: the fixed prefix holds both nonces and the header ciphertext, which
	// is everything needed before a body_len can be trusted or allocated.
	prefix, err := extractAt(img, keys.PosSeed, 0, payloadPrefix)
	if err != nil {
		return archive.Unpacked{}, errReveal()
	}
	headerNonce := prefix[:format.NonceSize]
	headerCT := prefix[format.NonceSize : format.NonceSize+format.HeaderCipherSize]
	bodyNonce := prefix[format.NonceSize+format.HeaderCipherSize:]

	header, err := crypto.OpenHeader(keys.EncKey, salt, headerNonce, headerCT)
	if err != nil {
		return archive.Unpacked{}, errReveal()
	}

	// Step 7: validate the authenticated body length against the carrier before
	// it is turned into a slice or an allocation.
	width, height := img.Rect.Dx(), img.Rect.Dy()
	if header.BodyLen > uint64(stego.Capacity(width, height)) {
		return archive.Unpacked{}, errReveal()
	}
	// Version and flag bits live inside the authenticated header, so reporting
	// them to the key holder leaks nothing.
	if header.Version != format.Version || header.Flags&^format.FlagZstd != 0 {
		return archive.Unpacked{}, fail(ExitDecrypt,
			"payload was written by an incompatible writer (v%d, flags %#02x)",
			header.Version, header.Flags)
	}

	// Step 8: the body continues the same pool, so its bits follow the prefix's.
	bodyCT, err := extractAt(img, keys.PosSeed, payloadPrefix, int(header.BodyLen)+format.TagSize)
	if err != nil {
		return archive.Unpacked{}, errReveal()
	}
	plain, err := crypto.OpenBody(keys.EncKey, headerCT, bodyNonce, bodyCT)
	if err != nil {
		return archive.Unpacked{}, errReveal()
	}

	// Step 10.
	unpacked, err := archive.Unpack(plain, dir, archive.UnpackOptions{
		Compressed: header.Flags&format.FlagZstd != 0,
		MaxBytes:   maxOutput,
		Force:      force,
	})
	if err != nil {
		return archive.Unpacked{}, unpackError(err)
	}
	return unpacked, nil
}

// extractAt reads size payload bytes starting at byte offset off of the STC
// stream. Every message byte is one independent block of STCWidth channels, and
// the offsets used here are multiples of STCWidth, so reading the prefix and the
// body as separate slices lands on the same block boundaries the writer used.
func extractAt(img *image.NRGBA, posSeed [32]byte, off, size int) ([]byte, error) {
	if size <= 0 {
		return nil, nil
	}
	end := off + size
	positions := stego.PoolPositions(stego.ChannelCount(img), posSeed, end*stego.STCWidth)
	if len(positions) < end*stego.STCWidth {
		return nil, stego.ErrTooSmall
	}
	return stego.ExtractSTC(img, positions[off*stego.STCWidth:], size)
}

// errReveal is the single failure of the decrypt path (§5, exit 3). A wrong
// passphrase, an image with nothing hidden in it, and a damaged blob are
// deliberately indistinguishable: the blob has no plaintext marker to tell them
// apart (§11.1).
func errReveal() error {
	return fail(ExitDecrypt, "cannot decrypt: wrong passphrase, or the image contains no hidden data")
}

// unpackError maps the archive refusals onto the documented exit codes. Only the
// destination collision has a remedy the user can apply, so it gets a hint.
func unpackError(err error) error {
	switch {
	case errors.Is(err, archive.ErrExists):
		return fail(ExitError, "%v; use --force to overwrite", err)
	case errors.Is(err, archive.ErrTooLarge):
		return fail(ExitError, "%v; raise --max-output to allow more", err)
	case errors.Is(err, archive.ErrUnsafePath):
		return fail(ExitError, "refusing to write outside the output directory: %v", err)
	default:
		return err
	}
}
