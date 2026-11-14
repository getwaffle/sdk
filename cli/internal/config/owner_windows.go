//go:build windows

package config

import "os"

// ownsDir: POSIX ownership/modes do not apply on Windows; behaviour is
// unchanged there.
func ownsDir(os.FileInfo) bool { return false }
