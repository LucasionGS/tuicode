package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
)

// sandboxRoot confines every file system mutation, git command and external
// program to one folder. Tests set it so random input can never touch real files.
var sandboxRoot string

// setSandbox enables the sandbox for a folder.
func setSandbox(root string) {
	sandboxRoot = root
	check := func(p string) error { return checkSandbox(p) }
	if root == "" {
		check = nil
	}
	buffer.WriteGuard = check
	git.Guard = check
}

func checkSandbox(path string) error {
	if sandboxRoot == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if abs == sandboxRoot || strings.HasPrefix(abs, sandboxRoot+string(filepath.Separator)) {
		return nil
	}
	return errors.New("Blocked by the test sandbox: " + abs)
}

func fsWriteFile(p string, data []byte, perm os.FileMode) error {
	if err := checkSandbox(p); err != nil {
		return err
	}
	return os.WriteFile(p, data, perm)
}

func fsRemoveAll(p string) error {
	if err := checkSandbox(p); err != nil {
		return err
	}
	return os.RemoveAll(p)
}

func fsRename(from, to string) error {
	if err := checkSandbox(from); err != nil {
		return err
	}
	if err := checkSandbox(to); err != nil {
		return err
	}
	return os.Rename(from, to)
}

func fsMkdirAll(p string, perm os.FileMode) error {
	if err := checkSandbox(p); err != nil {
		return err
	}
	return os.MkdirAll(p, perm)
}

func fsOpenFile(p string, flag int, perm os.FileMode) (*os.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		if err := checkSandbox(p); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(p, flag, perm)
}

// openExternal opens a URL or path with the desktop's default application.
func openExternal(target string) {
	if sandboxRoot != "" {
		return
	}
	_ = exec.Command("xdg-open", target).Start()
}
