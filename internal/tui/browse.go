package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// dirEntry is one row of a picker listing.
type dirEntry struct {
	name string
	dir  bool
	size int64
}

// picker is a single-directory listing plus the manual path field under it. It
// never walks a tree: one level at a time keeps the screen short and the keys
// obvious, and a recursive browser would need its own scrolling model.
type picker struct {
	dir     string
	filter  func(name string, dir bool) bool
	entries []dirEntry
	cursor  int
	top     int
	manual  string
	typing  bool
	note    string
}

// pngOnly is the carrier filter: directories stay navigable, files must be PNG.
func pngOnly(name string, dir bool) bool {
	return dir || strings.EqualFold(filepath.Ext(name), ".png")
}

// newPicker lists dir and drops the cursor on the first entry.
func newPicker(dir string, filter func(name string, dir bool) bool) picker {
	p := picker{dir: dir, filter: filter}
	p.reload()
	return p
}

// reload re-reads the current directory. Hidden entries are skipped: they are
// clutter in a picker, and `.wisanggeni-*` temporaries belong to the writer.
func (p *picker) reload() {
	p.note = ""
	items, err := os.ReadDir(p.dir)
	if err != nil {
		p.entries = nil
		p.cursor, p.top = 0, 0
		p.note = err.Error()
		return
	}

	entries := make([]dirEntry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if p.filter != nil && !p.filter(name, item.IsDir()) {
			continue
		}
		entry := dirEntry{name: name, dir: item.IsDir()}
		if info, err := item.Info(); err == nil {
			entry.size = info.Size()
		}
		entries = append(entries, entry)
	}
	// Directories first, then files, each alphabetical: the two kinds are
	// different verbs here, and mixing them makes the list feel random.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		return entries[i].name < entries[j].name
	})
	p.entries = entries
	if p.cursor >= len(entries) {
		p.cursor = len(entries) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if len(entries) == 0 {
		p.note = "nothing here"
	}
}

// move walks the cursor, wrapping at both ends so the list never dead-ends.
func (p *picker) move(delta int) {
	if len(p.entries) == 0 {
		return
	}
	p.cursor = (p.cursor + delta + len(p.entries)) % len(p.entries)
	p.note = ""
}

// current returns the highlighted entry.
func (p *picker) current() (dirEntry, bool) {
	if p.cursor < 0 || p.cursor >= len(p.entries) {
		return dirEntry{}, false
	}
	return p.entries[p.cursor], true
}

// descend opens the highlighted directory, or does nothing when the row is a
// file, which the caller treats as its own event.
func (p *picker) descend() {
	entry, ok := p.current()
	if !ok || !entry.dir {
		return
	}
	p.dir = filepath.Join(p.dir, entry.name)
	p.cursor, p.top = 0, 0
	p.reload()
}

// ascend goes one level up, stopping at the filesystem root.
func (p *picker) ascend() {
	parent := filepath.Dir(p.dir)
	if parent == p.dir {
		return
	}
	p.dir = parent
	p.cursor, p.top = 0, 0
	p.reload()
}

// window returns the indices of at most rows entries that keep the cursor
// visible, so the listing scrolls instead of overflowing the panel.
func (p *picker) window(rows int) []int {
	if rows <= 0 || len(p.entries) == 0 {
		return nil
	}
	if p.top > p.cursor {
		p.top = p.cursor
	}
	if p.cursor >= p.top+rows {
		p.top = p.cursor - rows + 1
	}
	if limit := len(p.entries) - rows; p.top > limit {
		p.top = limit
	}
	if p.top < 0 {
		p.top = 0
	}

	end := p.top + rows
	if end > len(p.entries) {
		end = len(p.entries)
	}
	indices := make([]int, 0, end-p.top)
	for i := p.top; i < end; i++ {
		indices = append(indices, i)
	}
	return indices
}

// appendManual resolves the typed path against the current directory and adds
// it to the field's target. It reports the path added, or "" with a note set.
func (p *picker) appendManual(accept func(path string) error) (string, bool) {
	text := strings.TrimSpace(p.manual)
	if text == "" {
		p.note = "type a path first"
		return "", false
	}
	path := text
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.dir, path)
	}
	path = filepath.Clean(path)
	if err := accept(path); err != nil {
		p.note = err.Error()
		return "", false
	}
	p.manual = ""
	p.note = ""
	return path, true
}

// shortenDir replaces the home prefix with ~ so long paths fit the panel.
func shortenDir(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(dir, home) {
		return dir
	}
	return "~" + strings.TrimPrefix(dir, home)
}
