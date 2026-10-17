package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tuicode/internal/git"
)

// blameEnabled reports whether either git blame decoration is turned on.
func (a *App) blameEnabled() bool {
	return a.settings.Bool("git.blame.editorDecoration.enabled", "") || a.settings.Bool("git.blame.statusBarItem.enabled", "")
}

// blameFor returns the blame of a line, refreshing the document's blame in the background when it is stale.
func (a *App) blameFor(d *Document, line int) (git.BlameInfo, bool) {
	if !a.blameEnabled() || a.gitRoot == "" || d.Buf.Path == "" || d.Virtual {
		return git.BlameInfo{}, false
	}
	if _, tracked := a.gitByPath[d.Buf.Path]; tracked && a.gitByPath[d.Buf.Path].Untracked() {
		return git.BlameInfo{}, false
	}
	version := d.Buf.Version() + 1 // 0 means never blamed
	if d.blameVersion != version && !d.blameRunning && time.Since(d.blameAsked) > 300*time.Millisecond {
		d.blameRunning = true
		d.blameAsked = time.Now()
		root := a.gitRoot
		rel, err := filepath.Rel(root, d.Buf.Path)
		if err == nil {
			var contents *string
			if d.Buf.Dirty() {
				text := d.Buf.Text()
				contents = &text
			}
			go func() {
				lines, err := git.BlameFile(root, rel, contents)
				a.Post(func() {
					d.blameRunning = false
					d.blameVersion = version
					if err != nil {
						d.blame = nil
						return
					}
					d.blame = lines
				})
			}()
		}
	}
	if line < 0 || line >= len(d.blame) {
		return git.BlameInfo{}, false
	}
	return d.blame[line], true
}

// formatBlame fills a VS Code blame template such as "${subject}, ${authorName} (${authorDateAgo})".
func formatBlame(tpl string, b git.BlameInfo) string {
	if b.Uncommitted() {
		return "Not Committed Yet"
	}
	hash := b.Hash
	if len(hash) > 8 {
		hash = hash[:8]
	}
	return strings.NewReplacer(
		"${hash}", b.Hash,
		"${hashShort}", hash,
		"${subject}", b.Summary,
		"${authorName}", b.Author,
		"${authorEmail}", b.Email,
		"${authorDate}", b.Time.Format("January 2, 2006"),
		"${authorDateAgo}", timeAgo(b.Time),
	).Replace(tpl)
}

// timeAgo formats a time like VS Code's fromNow: "3 days ago".
func timeAgo(t time.Time) string {
	d := time.Since(t)
	unit := func(n int, name string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s ago", name)
		}
		return fmt.Sprintf("%d %ss ago", n, name)
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return unit(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return unit(int(d.Hours()), "hour")
	case d < 7*24*time.Hour:
		return unit(int(d.Hours()/24), "day")
	case d < 30*24*time.Hour:
		return unit(int(d.Hours()/24/7), "week")
	case d < 365*24*time.Hour:
		return unit(int(d.Hours()/24/30), "month")
	}
	return unit(int(d.Hours()/24/365), "year")
}
