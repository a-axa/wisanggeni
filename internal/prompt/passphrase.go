// Package prompt obtains a passphrase for one operation. It supports the three
// mutually exclusive modes of §5: an interactive terminal prompt, a file, and a
// single line from stdin. A passphrase never travels through argv or the
// environment.
package prompt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/term"
)

// MinLength is the length below which Read reports a non-blocking warning (§5).
const MinLength = 12

var (
	// ErrNoSource reports that no source was selected and no terminal is
	// available to prompt on.
	ErrNoSource = errors.New("prompt: no passphrase source and no terminal available")

	// ErrEmpty reports a passphrase that is empty once the trailing newline is
	// removed.
	ErrEmpty = errors.New("prompt: empty passphrase")

	// ErrMismatch reports a failed interactive confirmation.
	ErrMismatch = errors.New("prompt: passphrases do not match")
)

// Terminal is an interactive terminal able to read a secret without echoing it.
type Terminal interface {
	// ReadPassword writes prompt, reads a secret without echo, and returns it.
	ReadPassword(prompt string) ([]byte, error)

	// Printf writes a line to the terminal, for prompts and notices.
	Printf(format string, args ...any)
}

// Options selects where the passphrase comes from.
type Options struct {
	// File is the path from --passphrase-file; empty when unset.
	File string

	// FromStdin is true for --passphrase-stdin.
	FromStdin bool

	// Confirm asks for the passphrase twice and compares the two. Only the
	// interactive mode honours it, which is where hide needs it.
	Confirm bool

	// In supplies --passphrase-stdin. Nil means os.Stdin.
	In io.Reader

	// Term is the interactive terminal. Nil resolves the process terminal
	// lazily; tests pass a stub.
	Term Terminal
}

// Result is a passphrase plus the warnings the caller should surface. Warnings
// are advisory: the passphrase is returned regardless.
type Result struct {
	Passphrase []byte
	Warnings   []string
}

// Read obtains a passphrase according to opts. File and stdin take precedence
// over the terminal; exactly one source is used.
func Read(opts Options) (Result, error) {
	switch {
	case opts.File != "":
		return readFile(opts.File)
	case opts.FromStdin:
		line, err := readLine(inOr(opts.In))
		if err != nil {
			return Result{}, err
		}
		return finish(line, nil)
	default:
		return readTerminal(opts)
	}
}

// readFile reads the whole file and drops a single trailing newline. Any other
// byte, including trailing spaces, belongs to the passphrase.
func readFile(path string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("prompt: passphrase file %q: %w", path, err)
	}

	var warnings []string
	if info, err := os.Stat(path); err == nil {
		if msg, ok := filePermWarning(info); ok {
			warnings = append(warnings, msg)
		}
	}
	return finish(stripOneNewline(raw), warnings)
}

// readLine reads one line, dropping a single trailing newline.
func readLine(r io.Reader) ([]byte, error) {
	line, err := bufio.NewReader(r).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("prompt: read stdin: %w", err)
	}
	return stripOneNewline(line), nil
}

// readTerminal prompts on an interactive terminal. Without one it reports
// ErrNoSource rather than falling back to a half-open stdin.
func readTerminal(opts Options) (Result, error) {
	term, closeFn, err := terminal(opts.Term)
	if err != nil {
		return Result{}, err
	}
	defer closeFn()

	first, err := term.ReadPassword("Passphrase: ")
	if err != nil {
		return Result{}, fmt.Errorf("prompt: read passphrase: %w", err)
	}
	pass := stripOneNewline(first)

	if opts.Confirm {
		second, err := term.ReadPassword("Confirm passphrase: ")
		if err != nil {
			return Result{}, fmt.Errorf("prompt: read confirmation: %w", err)
		}
		if !bytes.Equal(pass, stripOneNewline(second)) {
			return Result{}, ErrMismatch
		}
	}
	return finish(pass, nil)
}

// finish applies the shared rules: an empty passphrase is refused, and a short
// one is returned with a warning.
func finish(pass []byte, warnings []string) (Result, error) {
	if len(pass) == 0 {
		return Result{}, ErrEmpty
	}
	if len(pass) < MinLength {
		warnings = append(warnings, fmt.Sprintf(
			"passphrase is shorter than %d characters", MinLength))
	}
	return Result{Passphrase: pass, Warnings: warnings}, nil
}

// stripOneNewline removes exactly one trailing "\n" or "\r\n", nothing else.
func stripOneNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
		if len(b) > 0 && b[len(b)-1] == '\r' {
			b = b[:len(b)-1]
		}
	}
	return b
}

// inOr returns r, or os.Stdin when r is nil.
func inOr(r io.Reader) io.Reader {
	if r == nil {
		return os.Stdin
	}
	return r
}

// terminal returns the terminal to prompt on. A non-nil candidate is used
// as-is with no close function; otherwise the process terminal is resolved.
func terminal(candidate Terminal) (Terminal, func(), error) {
	if candidate != nil {
		return candidate, func() {}, nil
	}
	t, closer, err := resolveTerminal()
	if err != nil {
		return nil, func() {}, fmt.Errorf("%w: %w", ErrNoSource, err)
	}
	return t, closer, nil
}

// resolveTerminal is a variable so tests can simulate a machine without a
// controlling terminal.
var resolveTerminal = openTerminal

// fileTerminal is a Terminal backed by an *os.File. It is what both the
// process stdin and the controlling terminal resolve to.
type fileTerminal struct {
	in  *os.File
	out io.Writer
}

func (t *fileTerminal) ReadPassword(prompt string) ([]byte, error) {
	if _, err := fmt.Fprint(t.out, prompt); err != nil {
		return nil, err
	}
	secret, err := term.ReadPassword(int(t.in.Fd()))
	// The Enter the user pressed was not echoed either; move to a new line so
	// the terminal output stays readable.
	_, _ = fmt.Fprintln(t.out)
	return secret, err
}

func (t *fileTerminal) Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(t.out, format, args...)
}

// openTerminal resolves the process terminal. It prefers stdin when that is a
// terminal and otherwise opens the controlling terminal, so a piped stdin does
// not block an interactive prompt.
func openTerminal() (Terminal, func(), error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return &fileTerminal{in: os.Stdin, out: os.Stderr}, func() {}, nil
	}
	f, err := os.OpenFile(ttyName, os.O_RDWR, 0)
	if err != nil {
		return nil, func() {}, err
	}
	return &fileTerminal{in: f, out: f}, func() { _ = f.Close() }, nil
}

// filePermWarning reports a passphrase file whose mode is not 0600. It never
// fires on platforms without POSIX permission bits.
func filePermWarning(info fs.FileInfo) (string, bool) {
	perm, ok := posixFilePerm(info)
	if !ok || perm == 0o600 {
		return "", false
	}
	return fmt.Sprintf("passphrase file %q is mode %04o, expected 0600", info.Name(), perm), true
}
