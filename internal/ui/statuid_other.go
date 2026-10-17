//go:build !unix

package ui

import "os"

// statUID returns the owner of a file; ownership is not checked on this platform.
func statUID(st os.FileInfo) int { return os.Getuid() }
