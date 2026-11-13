package prompt_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/prompt"
)

// stubTerminal is a scripted Terminal: it returns the given secrets in order.
type stubTerminal struct {
	secrets [][]byte
	next    int
	Out     bytes.Buffer
}

func (s *stubTerminal) ReadPassword(p string) ([]byte, error) {
	s.Out.WriteString(p)
	if s.next >= len(s.secrets) {
		return nil, errors.New("stub: out of scripted secrets")
	}
	secret := s.secrets[s.next]
	s.next++
	return secret, nil
}

func (s *stubTerminal) Printf(format string, args ...any) {
	fmt.Fprintf(&s.Out, format, args...)
}

// writeFile writes data with mode 0600 unless told otherwise.
func writeFile(t *testing.T, mode os.FileMode, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return path
}

func TestReadFileKeepsTrailingSpaces(t *testing.T) {
	t.Parallel()
	path := writeFile(t, 0o600, []byte("  correct horse  \n"))

	got, err := prompt.Read(prompt.Options{File: path})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "  correct horse  " {
		t.Fatalf("passphrase = %q, want trailing spaces kept", got.Passphrase)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", got.Warnings)
	}
}

func TestReadFileStripsOnlyOneNewline(t *testing.T) {
	t.Parallel()
	path := writeFile(t, 0o600, []byte("one\ntwo\n"))

	got, err := prompt.Read(prompt.Options{File: path})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "one\ntwo" {
		t.Fatalf("passphrase = %q, want the inner newline kept", got.Passphrase)
	}
}

func TestReadFileCRLF(t *testing.T) {
	t.Parallel()
	path := writeFile(t, 0o600, []byte("secret\r\n"))

	got, err := prompt.Read(prompt.Options{File: path})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "secret" {
		t.Fatalf("passphrase = %q, want %q", got.Passphrase, "secret")
	}
}

func TestReadFileEmpty(t *testing.T) {
	t.Parallel()
	for _, data := range [][]byte{nil, []byte("\n"), []byte("\r\n")} {
		path := writeFile(t, 0o600, data)
		if _, err := prompt.Read(prompt.Options{File: path}); !errors.Is(err, prompt.ErrEmpty) {
			t.Fatalf("Read(%q) = %v, want ErrEmpty", data, err)
		}
	}
}

func TestReadFileMissing(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := prompt.Read(prompt.Options{File: missing}); err == nil {
		t.Fatal("missing file was accepted")
	}
}

func TestReadFilePermWarning(t *testing.T) {
	t.Parallel()
	path := writeFile(t, 0o644, []byte("long enough passphrase"))

	got, err := prompt.Read(prompt.Options{File: path})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	warned := strings.Contains(strings.Join(got.Warnings, "\n"), "0600")
	if runtime.GOOS == "windows" {
		// No POSIX bits: the warning cannot apply.
		if warned {
			t.Fatalf("unexpected permission warning on Windows: %v", got.Warnings)
		}
		return
	}
	if !warned {
		t.Fatalf("warnings = %v, want a 0600 note", got.Warnings)
	}
}

func TestReadShortPassphraseWarns(t *testing.T) {
	t.Parallel()
	path := writeFile(t, 0o600, []byte("short\n"))

	got, err := prompt.Read(prompt.Options{File: path})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "short" {
		t.Fatalf("passphrase = %q", got.Passphrase)
	}
	if !strings.Contains(strings.Join(got.Warnings, " "), "shorter than") {
		t.Fatalf("warnings = %v, want a length note", got.Warnings)
	}
}

func TestReadStdin(t *testing.T) {
	t.Parallel()
	got, err := prompt.Read(prompt.Options{
		FromStdin: true,
		In:        strings.NewReader("pipe secret \r\nignored line\n"),
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "pipe secret " {
		t.Fatalf("passphrase = %q, want the first line only, spaces kept", got.Passphrase)
	}
}

func TestReadStdinWithoutNewline(t *testing.T) {
	t.Parallel()
	got, err := prompt.Read(prompt.Options{FromStdin: true, In: strings.NewReader("abc")})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "abc" {
		t.Fatalf("passphrase = %q", got.Passphrase)
	}
}

// TestLookExplicitSources covers the sources that are known without touching the
// terminal. The no-flag case depends on the machine running the test, so it is
// deliberately not asserted here.
func TestLookExplicitSources(t *testing.T) {
	t.Parallel()
	term := &stubTerminal{}
	for _, tc := range []struct {
		name string
		opts prompt.Options
		want bool
	}{
		{name: "file", opts: prompt.Options{File: "/tmp/does-not-need-to-exist"}, want: true},
		{name: "stdin", opts: prompt.Options{FromStdin: true}, want: true},
		{name: "terminal stub", opts: prompt.Options{Term: term}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := prompt.Look(tc.opts); got != tc.want {
				t.Fatalf("Look(%+v) = %v, want %v", tc.opts, got, tc.want)
			}
		})
	}
}

func TestReadTerminalConfirm(t *testing.T) {
	t.Parallel()
	term := &stubTerminal{secrets: [][]byte{[]byte("a strong passphrase"), []byte("a strong passphrase")}}

	got, err := prompt.Read(prompt.Options{Confirm: true, Term: term})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) != "a strong passphrase" {
		t.Fatalf("passphrase = %q", got.Passphrase)
	}
	if !strings.Contains(term.Out.String(), "Confirm") {
		t.Fatalf("confirmation prompt missing: %q", term.Out.String())
	}
}

func TestReadTerminalConfirmMismatch(t *testing.T) {
	t.Parallel()
	term := &stubTerminal{secrets: [][]byte{[]byte("aaaaaaaaaaaa"), []byte("bbbbbbbbbbbb")}}

	if _, err := prompt.Read(prompt.Options{Confirm: true, Term: term}); !errors.Is(err, prompt.ErrMismatch) {
		t.Fatalf("Read = %v, want ErrMismatch", err)
	}
}

func TestReadTerminalEmptyRejected(t *testing.T) {
	t.Parallel()
	term := &stubTerminal{secrets: [][]byte{{}}}

	if _, err := prompt.Read(prompt.Options{Term: term}); !errors.Is(err, prompt.ErrEmpty) {
		t.Fatalf("Read = %v, want ErrEmpty", err)
	}
}

// TestReadNeverUsesEnvironment pins the §5 rule that no passphrase path goes
// through the environment: a planted variable must not influence the result.
func TestReadNeverUsesEnvironment(t *testing.T) {
	for _, key := range []string{
		"WISANGGENI_PASSPHRASE", "PASSPHRASE", "WISANGGENI_PASS",
	} {
		t.Setenv(key, "from-the-environment")
	}
	term := &stubTerminal{secrets: [][]byte{[]byte("from-the-terminal")}}

	got, err := prompt.Read(prompt.Options{Term: term})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Passphrase) == "from-the-environment" {
		t.Fatal("environment variable leaked into the passphrase")
	}
	if string(got.Passphrase) != "from-the-terminal" {
		t.Fatalf("passphrase = %q", got.Passphrase)
	}
}
