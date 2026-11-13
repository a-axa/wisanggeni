//go:build windows

package prompt

import "io/fs"

// ttyName is the console input device on Windows.
const ttyName = "CONIN$"

// posixFilePerm reports "unavailable": Windows has no POSIX permission bits,
// so the 0600 warning does not apply.
func posixFilePerm(fs.FileInfo) (fs.FileMode, bool) { return 0, false }
