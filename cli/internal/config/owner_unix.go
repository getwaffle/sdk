//go:build !windows

package config

import (
	"os"
	"syscall"
)

// ownsDir reports whether the current user owns the directory.
func ownsDir(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
