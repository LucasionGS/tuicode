//go:build unix

package ui

import (
	"os"
	"syscall"
)

// statUID returns the owner of a file.
func statUID(st os.FileInfo) int {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid)
	}
	return -1
}
