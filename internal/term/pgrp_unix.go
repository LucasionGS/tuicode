//go:build unix

package term

import (
	"os"

	"golang.org/x/sys/unix"
)

func foregroundPgrp(f *os.File) (int, error) {
	return unix.IoctlGetInt(int(f.Fd()), unix.TIOCGPGRP)
}
