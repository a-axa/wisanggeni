package cmd

import (
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/spf13/cobra"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/prompt"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// inspectPromptLabel tells the user that an empty answer is a valid choice here,
// unlike hide and reveal where it is refused.
const inspectPromptLabel = "Passphrase (Enter to skip): "

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <image.png>",
		Short: "Show carrier and payload information",
		Long: `Inspect reports what a PNG is and what it hides.

Without a passphrase it prints the carrier layer only — dimensions, capacity,
cover score, alpha and colour chunks — exactly like "capacity". With one it also
decrypts the payload and lists the archive in memory, writing nothing to disk.
The full mode costs as much as reveal: it pays Argon2, decrypts header and body,
decompresses, and parses the tar stream.

There is no flag to obtain a passphrase: when none is given and stdin is not a
terminal the command reports the carrier and stops, and at the prompt an empty
answer means the same thing.`,
		Args: cobra.ExactArgs(1),
		RunE: runInspect,
	}

	cmd.Flags().Bool("header-only", false, "decrypt only the header (version, flags, body_len)")
	addPassphraseFlags(cmd)

	return cmd
}

// promptAvailable reports whether an interactive prompt is possible. It is a
// variable so tests exercise the non-interactive path instead of blocking on the
// terminal of whoever runs them.
var promptAvailable = func() bool { return prompt.Look(prompt.Options{}) }

// promptPassphrase is the interactive read, a variable for the same reason.
var promptPassphrase = func() (prompt.Result, error) {
	return prompt.Read(prompt.Options{Label: inspectPromptLabel})
}

// runInspect drives §7.10: the carrier layer always, the payload layer only when
// a passphrase is available.
func runInspect(cmd *cobra.Command, args []string) error {
	car, err := openCarrierForReport(cmd, args[0])
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	printCarrier(out, args[0], car)

	headerOnly := flagBool(cmd, "header-only")
	passphrase, err := inspectPassphrase(cmd)
	if err != nil {
		return err
	}
	if passphrase == nil {
		if headerOnly {
			return fail(ExitError, "--header-only needs a passphrase")
		}
		return nil
	}
	defer clear(passphrase)

	return printPayload(out, car.Image, passphrase, headerOnly)
}

// inspectPassphrase resolves the passphrase for inspect. Unlike hide and reveal,
// inspect is useful without one, so nil means "report the carrier only": no
// source and no terminal (§7.10), or an empty answer at the prompt. A source the
// user named explicitly that then fails is still an error.
func inspectPassphrase(cmd *cobra.Command) ([]byte, error) {
	file := flagString(cmd, "passphrase-file")
	fromStdin := flagBool(cmd, "passphrase-stdin")

	if file != "" || fromStdin {
		result, err := prompt.Read(prompt.Options{File: file, FromStdin: fromStdin})
		if err != nil {
			return nil, err
		}
		inspectWarnings(cmd, result)
		return result.Passphrase, nil
	}

	if !promptAvailable() {
		return nil, nil
	}
	result, err := promptPassphrase()
	if errors.Is(err, prompt.ErrEmpty) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inspectWarnings(cmd, result)
	return result.Passphrase, nil
}

// inspectWarnings surfaces the advisory passphrase notes the way the other
// commands do.
func inspectWarnings(cmd *cobra.Command, result prompt.Result) {
	warn := notifier(cmd, false)
	for _, msg := range result.Warnings {
		warn("%s", msg)
	}
}

// printPayload runs the decrypt path of §7.9 against an already decoded carrier,
// minus the extraction to disk, and reports what the blob holds. Every failure
// before the archive parse collapses into the same opaque error as reveal, so
// inspect cannot be used as an oracle.
func printPayload(out io.Writer, img *image.NRGBA, passphrase []byte, headerOnly bool) error {
	salt, err := stego.ExtractSalt(img, format.SaltSize)
	if err != nil {
		return errReveal()
	}
	keys, err := crypto.Derive(string(passphrase), salt)
	if err != nil {
		return err
	}

	prefix, err := extractAt(img, keys.PosSeed, 0, payloadPrefix)
	if err != nil {
		return errReveal()
	}
	headerNonce := prefix[:format.NonceSize]
	headerCT := prefix[format.NonceSize : format.NonceSize+format.HeaderCipherSize]
	bodyNonce := prefix[format.NonceSize+format.HeaderCipherSize:]

	header, err := crypto.OpenHeader(keys.EncKey, salt, headerNonce, headerCT)
	if err != nil {
		return errReveal()
	}

	// Validate the authenticated body length against the carrier before it is
	// turned into a slice, then reject a payload this writer cannot read.
	width, height := img.Rect.Dx(), img.Rect.Dy()
	if header.BodyLen > uint64(stego.Capacity(width, height)) {
		return errReveal()
	}
	if header.Version != format.Version || header.Flags&^format.FlagZstd != 0 {
		return fail(ExitDecrypt,
			"payload was written by an incompatible writer (v%d, flags %#02x)",
			header.Version, header.Flags)
	}
	compressed := header.Flags&format.FlagZstd != 0

	_, _ = fmt.Fprintf(out, "payload       %s\n", payloadState(headerOnly))
	_, _ = fmt.Fprintf(out, "version       %d\n", header.Version)
	_, _ = fmt.Fprintf(out, "flags         %s\n", compressionName(compressed))
	_, _ = fmt.Fprintf(out, "body          %d bytes\n", header.BodyLen)
	if headerOnly {
		return nil
	}

	bodyCT, err := extractAt(img, keys.PosSeed, payloadPrefix, int(header.BodyLen)+format.TagSize)
	if err != nil {
		return errReveal()
	}
	plain, err := crypto.OpenBody(keys.EncKey, headerCT, bodyNonce, bodyCT)
	if err != nil {
		return errReveal()
	}
	defer clear(plain)

	// List rather than Unpack: the names are the point, and nothing may be
	// written to disk on the way to printing them.
	entries, err := archive.List(plain, archive.ListOptions{Compressed: compressed})
	if err != nil {
		return unpackError(err)
	}
	printEntries(out, entries, header.BodyLen, compressed)
	return nil
}

// printEntries lists the archive members and the sizes they add up to.
func printEntries(out io.Writer, entries []archive.Entry, bodyLen uint64, compressed bool) {
	var (
		total int64
		files int
	)
	for _, e := range entries {
		if !e.Dir {
			files++
			total += e.Size
		}
	}
	_, _ = fmt.Fprintf(out, "entries       %d (%d files)\n", len(entries), files)
	for _, e := range entries {
		if e.Dir {
			_, _ = fmt.Fprintf(out, "  %s (directory)\n", e.Name)
			continue
		}
		_, _ = fmt.Fprintf(out, "  %s  %d bytes\n", e.Name, e.Size)
	}
	_, _ = fmt.Fprintf(out, "total         %d bytes uncompressed\n", total)

	// The ratio only means something when the body really is compressed: without
	// zstd the body is the tar stream, whose padding and headers are not content.
	if compressed && bodyLen > 0 {
		_, _ = fmt.Fprintf(out, "ratio         %.2fx (content : body)\n",
			float64(total)/float64(bodyLen))
	}
}

// payloadState labels the payload layer, distinguishing the two depths.
func payloadState(headerOnly bool) string {
	if headerOnly {
		return "header only"
	}
	return "decrypted"
}

// compressionName renders the one flag bit the container defines.
func compressionName(compressed bool) string {
	if compressed {
		return "zstd"
	}
	return "none"
}
