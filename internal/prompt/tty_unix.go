//go:build unix

package prompt

import (
	"io/fs"
	"syscall"
)

// ttyName is the controlling terminal device on POSIX systems.
const ttyName = "/dev/tty"

// posixFilePerm returns the permission bits recorded by the kernel.
func posixFilePerm(info fs.FileInfo) (fs.FileMode, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return fs.FileMode(st.Mode) & fs.ModePerm, true
}
