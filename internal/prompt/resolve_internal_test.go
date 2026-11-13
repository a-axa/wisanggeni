package prompt

import (
	"errors"
	"testing"
)

// TestReadNoTerminal covers the §5 rule that, with no flag and no terminal, the
// reader fails instead of blocking or silently reading an empty secret.
func TestReadNoTerminal(t *testing.T) {
	orig := resolveTerminal
	t.Cleanup(func() { resolveTerminal = orig })
	resolveTerminal = func() (Terminal, func(), error) {
		return nil, func() {}, errors.New("simulated: no controlling terminal")
	}

	if _, err := Read(Options{}); !errors.Is(err, ErrNoSource) {
		t.Fatalf("Read = %v, want ErrNoSource", err)
	}
}

// TestReadClosesResolvedTerminal makes sure a terminal the reader opened is
// closed again.
func TestReadClosesResolvedTerminal(t *testing.T) {
	orig := resolveTerminal
	t.Cleanup(func() { resolveTerminal = orig })

	closed := false
	resolveTerminal = func() (Terminal, func(), error) {
		return &stubTerm{}, func() { closed = true }, nil
	}

	if _, err := Read(Options{}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !closed {
		t.Fatal("terminal was not closed")
	}
}

// stubTerm returns one fixed secret.
type stubTerm struct{}

func (stubTerm) ReadPassword(string) ([]byte, error) { return []byte("twelve chars"), nil }
func (stubTerm) Printf(string, ...any)               {}

func TestStripOneNewline(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"\n", ""},
		{"\r\n", ""},
		{"a\n", "a"},
		{"a\r\n", "a"},
		{"a\r", "a\r"},
		{"a\n\n", "a\n"},
		{"a\nb", "a\nb"},
	}
	for _, tc := range cases {
		if got := string(stripOneNewline([]byte(tc.in))); got != tc.want {
			t.Fatalf("stripOneNewline(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
