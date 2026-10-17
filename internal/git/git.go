package git

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FileStatus is one entry from git status.
type FileStatus struct {
	Path     string // relative to the repository root
	OrigPath string
	X        byte // index status
	Y        byte // worktree status
}

// Staged reports whether the entry has staged changes.
func (f FileStatus) Staged() bool { return f.X != ' ' && f.X != '?' && f.X != '!' }

// Unstaged reports whether the entry has worktree changes (including untracked).
func (f FileStatus) Unstaged() bool { return f.Y != ' ' }

// Untracked reports whether the file is untracked.
func (f FileStatus) Untracked() bool { return f.X == '?' }

// Conflicted reports whether the entry is a merge conflict.
func (f FileStatus) Conflicted() bool {
	return f.X == 'U' || f.Y == 'U' || (f.X == 'A' && f.Y == 'A') || (f.X == 'D' && f.Y == 'D')
}

// Letter returns the VS Code style decoration letter for the entry.
func Letter(code byte) string {
	switch code {
	case 'M':
		return "M"
	case 'A':
		return "A"
	case 'D':
		return "D"
	case 'R':
		return "R"
	case 'C':
		return "C"
	case '?':
		return "U"
	case 'U':
		return "!"
	case 'T':
		return "T"
	}
	return ""
}

// Guard, when set, can veto running git in a folder (used by tests to sandbox).
var Guard func(dir string) error

// Run executes git in dir and returns stdout.
func Run(dir string, args ...string) (string, error) {
	if Guard != nil {
		if err := Guard(dir); err != nil {
			return "", err
		}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

// Available reports whether git is installed.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// RepoRoot returns the repository root containing dir.
func RepoRoot(dir string) (string, bool) {
	if !Available() {
		return "", false
	}
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// Branch returns the current branch name (or short commit when detached).
func Branch(root string) string {
	out, err := Run(root, "symbolic-ref", "--short", "-q", "HEAD")
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	out, err = Run(root, "rev-parse", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

// AheadBehind returns how many commits HEAD is ahead/behind its upstream.
func AheadBehind(root string) (ahead, behind int, hasUpstream bool) {
	out, err := Run(root, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		behind, _ = strconv.Atoi(f[0])
		ahead, _ = strconv.Atoi(f[1])
	}
	return ahead, behind, true
}

// Status returns the working tree status.
func Status(root string) ([]FileStatus, error) {
	out, err := Run(root, "status", "--porcelain=v1", "-z", "-uall")
	if err != nil {
		return nil, err
	}
	var res []FileStatus
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		fs := FileStatus{X: p[0], Y: p[1], Path: p[3:]}
		if fs.X == 'R' || fs.X == 'C' {
			if i+1 < len(parts) {
				fs.OrigPath = parts[i+1]
				i++
			}
		}
		res = append(res, fs)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Path < res[j].Path })
	return res, nil
}

// Show returns the content of a file at a revision ("HEAD", "" for the index).
func Show(root, rev, rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	return Run(root, "show", rev+":"+rel)
}

// Stage adds paths to the index.
func Stage(root string, paths ...string) error {
	_, err := Run(root, append([]string{"add", "-A", "--"}, paths...)...)
	return err
}

// Unstage removes paths from the index.
func Unstage(root string, paths ...string) error {
	_, err := Run(root, append([]string{"reset", "-q", "HEAD", "--"}, paths...)...)
	if err != nil {
		// repository without commits
		_, err = Run(root, append([]string{"rm", "--cached", "-r", "-q", "--"}, paths...)...)
	}
	return err
}

// Discard reverts worktree changes for paths. Untracked files are deleted.
func Discard(root string, files []FileStatus) error {
	var tracked []string
	for _, f := range files {
		if f.Untracked() {
			if Guard != nil {
				if err := Guard(filepath.Join(root, f.Path)); err != nil {
					return err
				}
			}
			if err := os.RemoveAll(filepath.Join(root, f.Path)); err != nil {
				return err
			}
			continue
		}
		tracked = append(tracked, f.Path)
	}
	if len(tracked) > 0 {
		_, err := Run(root, append([]string{"checkout", "--"}, tracked...)...)
		return err
	}
	return nil
}

// Commit creates a commit with message.
func Commit(root, message string, all, amend bool) error {
	args := []string{"commit", "-m", message}
	if all {
		args = append(args, "-a")
	}
	if amend {
		args = append(args, "--amend")
	}
	_, err := Run(root, args...)
	return err
}

// Branches lists local and remote branches.
func Branches(root string) ([]string, error) {
	out, err := Run(root, "branch", "-a", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var res []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasSuffix(l, "/HEAD") {
			res = append(res, l)
		}
	}
	return res, nil
}

// Log returns recent commit subjects.
func Log(root string, n int) ([]string, error) {
	out, err := Run(root, "log", "--oneline", "-n", strconv.Itoa(n))
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// BlameLine returns a short blame annotation for one line (1-based).
func BlameLine(root, rel string, line int) string {
	out, err := Run(root, "blame", "-L", strconv.Itoa(line)+","+strconv.Itoa(line), "--porcelain", "--", rel)
	if err != nil {
		return ""
	}
	var author, summary, when string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "author "):
			author = strings.TrimPrefix(l, "author ")
		case strings.HasPrefix(l, "summary "):
			summary = strings.TrimPrefix(l, "summary ")
		case strings.HasPrefix(l, "author-time "):
			when = strings.TrimPrefix(l, "author-time ")
		}
	}
	if author == "" {
		return ""
	}
	if author == "Not Committed Yet" {
		return "You, Uncommitted changes"
	}
	_ = when
	return author + ", " + summary
}

// BlameInfo is the commit that last changed a line.
type BlameInfo struct {
	Hash    string
	Author  string
	Email   string
	Time    time.Time
	Summary string
}

// Uncommitted reports whether the line has not been committed yet.
func (b BlameInfo) Uncommitted() bool { return strings.Trim(b.Hash, "0") == "" }

// BlameFile blames every line of a file. contents, when not nil, is the current (possibly unsaved) text.
func BlameFile(root, rel string, contents *string) ([]BlameInfo, error) {
	if Guard != nil {
		if err := Guard(root); err != nil {
			return nil, err
		}
	}
	args := []string{"blame", "--porcelain"}
	if contents != nil {
		args = append(args, "--contents", "-")
	}
	args = append(args, "--", rel)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if contents != nil {
		cmd.Stdin = strings.NewReader(*contents)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	commits := map[string]*BlameInfo{}
	var lines []BlameInfo
	var cur *BlameInfo
	for _, l := range strings.Split(string(out), "\n") {
		if l == "" {
			continue
		}
		if l[0] == '\t' {
			// the line's content ends its entry
			if cur != nil {
				lines = append(lines, *cur)
			}
			continue
		}
		fields := strings.Fields(l)
		if len(fields) >= 3 && len(fields[0]) == 40 {
			if c, ok := commits[fields[0]]; ok {
				cur = c
			} else {
				cur = &BlameInfo{Hash: fields[0]}
				commits[fields[0]] = cur
			}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, _ := strings.Cut(l, " ")
		switch key {
		case "author":
			cur.Author = val
		case "author-mail":
			cur.Email = strings.Trim(val, "<>")
		case "author-time":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				cur.Time = time.Unix(n, 0)
			}
		case "summary":
			cur.Summary = val
		}
	}
	return lines, nil
}
