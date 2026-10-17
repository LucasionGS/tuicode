package ui

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"tuicode/internal/buffer"
)

//go:embed kitty-tuicode.conf
var kittyConf string

func kittyConfigDir() string {
	if d := os.Getenv("KITTY_CONFIG_DIRECTORY"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "kitty")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "kitty")
}

// installKittyPassthrough writes tuicode.conf next to kitty.conf and includes it, after asking.
func (a *App) installKittyPassthrough() {
	dir := kittyConfigDir()
	main := filepath.Join(dir, "kitty.conf")
	snippet := filepath.Join(dir, "tuicode.conf")
	a.showDialog("Let VS Code shortcuts reach tuicode in kitty?",
		"This writes "+snippet+" and adds \"include tuicode.conf\" to "+main+". kitty keeps its own shortcuts outside tuicode. Reload kitty's config afterwards (ctrl+shift+F5).",
		[]string{"Install", "Show Snippet", "Cancel"}, func(c int) {
			switch c {
			case 0:
				if err := fsMkdirAll(dir, 0o755); err != nil {
					a.notify(NotifyError, err.Error())
					return
				}
				if err := fsWriteFile(snippet, []byte(kittyConf), 0o644); err != nil {
					a.notify(NotifyError, err.Error())
					return
				}
				data, _ := os.ReadFile(main)
				if !strings.Contains(string(data), "include tuicode.conf") {
					f, err := fsOpenFile(main, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
					if err != nil {
						a.notify(NotifyError, err.Error())
						return
					}
					_, _ = f.WriteString("\n# Pass VS Code shortcuts through to tuicode\ninclude tuicode.conf\n")
					f.Close()
				}
				a.notify(NotifyInfo, "Installed. Reload kitty's configuration with ctrl+shift+F5 (or restart kitty).")
			case 1:
				a.untitledWithText("tuicode.conf", kittyConf)
			}
		})
}

// untitledWithText opens a new untitled editor with content.
func (a *App) untitledWithText(name, text string) {
	v := a.NewUntitled()
	v.buf().UntitledName = name
	v.buf().ApplyEdits([]buffer.Edit{{Text: text}}, nil, "")
	v.buf().MarkSaved()
	v.SetCursor(v.buf().Clamp(v.Cursor()))
}

// suggestKittySetup offers the pass-through config once when running inside kitty without it.
func (a *App) suggestKittySetup() {
	if os.Getenv("KITTY_WINDOW_ID") == "" || !a.settings.Bool("tuicode.suggestKittyConfig", "") {
		return
	}
	data, err := os.ReadFile(filepath.Join(kittyConfigDir(), "kitty.conf"))
	if err == nil && (strings.Contains(string(data), "include tuicode.conf") || strings.Contains(string(data), "clear_all_shortcuts yes")) {
		return
	}
	a.notify(NotifyInfo, "kitty's ctrl+shift shortcuts hide some VS Code shortcuts (like Ctrl+Shift+P). Let them pass through to tuicode?",
		NotifyAction{Label: "Set Up", Run: a.installKittyPassthrough},
		NotifyAction{Label: "Don't Show Again", Run: func() { _ = setUserSetting(a, "tuicode.suggestKittyConfig", false) }})
}
