package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/hide"
	"github.com/a-axa/wisanggeni/internal/prompt"
	"github.com/a-axa/wisanggeni/internal/reveal"
)

// maxRevealOutput caps the decompressed archive size, matching the CLI default
// (§5). The wizard exposes no knob for it: it is a guard, not a preference.
const maxRevealOutput int64 = 1 << 30

// hideStep is one screen of the hide wizard. The order is the order of §7.8:
// inputs, carrier, the report, the secret, the work, the result.
type hideStep int

const (
	stepInput hideStep = iota
	stepCarrier
	stepReport
	stepSecret
	stepWorking
	stepDone
)

// hideStepCount is the number of steps the header counts, excluding the two
// automatic ones: the user drives four, and Working/Done are consequences.
const hideStepCount = 4

// inputItem is a chosen input plus the one-line summary shown beside it. The
// summary is computed once, when it is picked, because counting a folder's files
// on every frame would hit the disk at animation speed.
type inputItem struct {
	path string
	info string
}

// hideWiz is the whole state of the hide flow. It holds no terminal and no
// goroutine: everything it needs to advance is in Update.
type hideWiz struct {
	step    hideStep
	pick    picker
	carrier picker

	inputs      []inputItem
	carrierPath string
	output      string
	force       bool

	pass    []byte
	confirm []byte
	field   int

	// secretNote is the advice shown under the fields: a short passphrase, or
	// the reason Enter did nothing.
	secretNote string

	plan       *hide.Plan
	planErr    error
	planOutput string
	analyzing  bool

	runErr    error
	result    hide.Result
	stage     hide.Stage
	stageDone map[hide.Stage]bool
}

// newHideWiz starts the flow in the working directory with the default output
// name the design uses.
func newHideWiz() hideWiz {
	dir := workingDir()
	return hideWiz{
		pick:      newPicker(dir, nil),
		carrier:   newPicker(dir, pngOnly),
		output:    "result.png",
		stageDone: map[hide.Stage]bool{},
	}
}

// workingDir is where the pickers open. A failure is not worth reporting: the
// picker's own note says "nothing here" and typing a path still works.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// reset returns the wizard to its first step, dropping the passphrase. It is
// what leaves the flow in a clean state when the user comes back to the menu.
func (w *hideWiz) reset() {
	fresh := newHideWiz()
	*w = fresh
}

// addInput picks a path as an input, or drops it when it is already chosen.
func (w *hideWiz) addInput(path string) {
	for i, item := range w.inputs {
		if item.path == path {
			w.inputs = append(w.inputs[:i], w.inputs[i+1:]...)
			return
		}
	}
	w.inputs = append(w.inputs, inputItem{path: path, info: summarize(path)})
}

// planStale reports whether the report on screen still describes what the user
// has chosen. The output path matters because Analyze checks it for collisions.
func (w *hideWiz) planStale() bool {
	return w.plan == nil || w.output != w.planOutput
}

// summarize describes an input for the selection list.
func summarize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	if !info.IsDir() {
		return humanBytes(info.Size())
	}
	files, total := walkDir(path)
	return fmt.Sprintf("folder, %d files, %s", files, humanBytes(total))
}

// walkDir counts a folder's files and their total size, ignoring anything it
// cannot read: this is a label, not an audit.
func walkDir(root string) (int, int64) {
	var files int
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		}
		files++
		return nil
	})
	return files, total
}

// hideOptions assembles the engine call the CLI would make. Force is the single
// checkbox the wizard exposes, exactly as `--force` covers all three overrides.
func (w *hideWiz) hideOptions() hide.Options {
	opts := hide.Options{
		CarrierPath: w.carrierPath,
		OutputPath:  w.output,
		Force:       w.force,
		Passphrase:  w.pass,
	}
	for _, item := range w.inputs {
		opts.Inputs = append(opts.Inputs, item.path)
	}
	return opts
}

// planMsg carries an Analyze result back to the UI goroutine.
type planMsg struct {
	seq  int
	plan *hide.Plan
	err  error
}

// runMsg carries a completed hide.
type runMsg struct {
	seq    int
	result hide.Result
	err    error
}

// stageMsg announces that Execute moved on to another stage.
type stageMsg struct {
	seq   int
	stage hide.Stage
}

// analyzeCmd runs the cheap half of §7.8 off the UI goroutine. It decodes the
// carrier and packs the payload, which is far too slow for Update.
func analyzeCmd(seq int, opts hide.Options) tea.Cmd {
	return func() tea.Msg {
		plan, err := hide.Analyze(opts)
		return planMsg{seq: seq, plan: plan, err: err}
	}
}

// runCmd runs the secret half. send, when set, forwards stage updates while the
// command is still working, which is what drives the Working screen.
func runCmd(seq int, plan *hide.Plan, opts hide.Options, send func(tea.Msg)) tea.Cmd {
	return func() tea.Msg {
		if send != nil {
			opts.Progress = func(stage hide.Stage) { send(stageMsg{seq: seq, stage: stage}) }
		}
		result, err := hide.Execute(plan, opts)
		return runMsg{seq: seq, result: result, err: err}
	}
}

// revealStep is one screen of the reveal flow (§8).
type revealStep int

const (
	revealPick revealStep = iota
	revealSecret
	revealWorking
	revealDone
)

// revealWiz is the whole state of the reveal flow.
type revealWiz struct {
	step    revealStep
	pick    picker
	image   string
	outDir  string
	force   bool
	pass    []byte
	field   int
	result  reveal.Result
	err     error
	working bool
}

func newRevealWiz() revealWiz {
	return revealWiz{
		pick:   newPicker(workingDir(), pngOnly),
		outDir: ".",
	}
}

func (w *revealWiz) reset() { *w = newRevealWiz() }

// revealMsg carries a completed extraction.
type revealMsg struct {
	seq    int
	result reveal.Result
	err    error
}

// revealCmd loads the carrier and decrypts it off the UI goroutine.
func revealCmd(seq int, image, dir string, force bool, pass []byte) tea.Cmd {
	return func() tea.Msg {
		car, err := carrier.Load(image)
		if err != nil {
			return revealMsg{seq: seq, err: err}
		}
		result, err := reveal.Open(car.Image, reveal.Options{
			Dir:        dir,
			Force:      force,
			MaxOutput:  maxRevealOutput,
			Passphrase: pass,
		})
		return revealMsg{seq: seq, result: result, err: err}
	}
}

// failureRow renders an error the way the CLI reports it: the message as the
// engine wrote it, plus the exit code, so both surfaces can be compared. The
// message is clipped but the code never is: the width is the panel's, and a
// wrapped error row would push the frame off screen.
func failureRow(err error, inner int) string {
	if err == nil {
		return ""
	}
	line := err.Error()
	code := exitcode.Code(err)
	if code == exitcode.OK {
		return styleErr.Render(clip(line, inner))
	}
	tag := fmt.Sprintf("  [exit %d]", code)
	return styleErr.Render(clip(line, inner-len(tag))) + styleErr.Render(tag)
}

// passphraseNote checks the two fields before the job is allowed to start. An
// empty or mismatched pair blocks; a short one only warns, exactly as the CLI
// prompt does (§5).
func passphraseNote(pass, confirm []byte) (string, bool) {
	switch {
	case len(pass) == 0:
		return "the passphrase cannot be empty", false
	case len(confirm) == 0:
		return "type it twice to confirm", false
	case !equalBytes(pass, confirm):
		return "the passphrases do not match", false
	case len(pass) < prompt.MinLength:
		return fmt.Sprintf("passphrase is shorter than %d characters", prompt.MinLength), true
	default:
		return "", true
	}
}

// errEmptyPassphrase is the reveal guard, worded and coded like the CLI's.
var errEmptyPassphrase = exitcode.Fail(exitcode.Error, "the passphrase cannot be empty")

// equalBytes compares two byte slices.
func equalBytes(a, b []byte) bool { return string(a) == string(b) }

// passphraseStrength is a coarse reading of a passphrase for the strength meter.
// It is a nudge, not a measure: length dominates, character classes add a little.
func passphraseStrength(pass []byte) (string, float64) {
	if len(pass) == 0 {
		return "empty", 0
	}
	var classes int
	var lower, upper, digit, other bool
	for _, r := range string(pass) {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			other = true
		}
	}
	for _, seen := range []bool{lower, upper, digit, other} {
		if seen {
			classes++
		}
	}
	// 40 characters with every class lands at 1.0; anything past that is clamped.
	score := float64(len(pass))/40*0.7 + float64(classes)/4*0.3
	if score > 1 {
		score = 1
	}
	switch {
	case score < 0.34:
		return "weak", score
	case score < 0.67:
		return "fair", score
	default:
		return "strong", score
	}
}

// countWords guesses how many words a passphrase holds, for the mockup's
// "5 words" hint. It is shown only when there is more than one.
func countWords(pass []byte) int {
	return len(strings.Fields(string(pass)))
}
