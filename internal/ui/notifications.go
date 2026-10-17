package ui

import (
	"time"

	"tuicode/internal/config"
)

// Notification levels.
const (
	NotifyInfo = iota
	NotifyWarning
	NotifyError
)

// Notification is a toast in the bottom right corner.
type Notification struct {
	Level   int
	Message string
	Source  string
	Actions []NotifyAction
	created time.Time
	sticky  bool
}

// NotifyAction is a button on a notification.
type NotifyAction struct {
	Label string
	Run   func()
}

func (a *App) notify(level int, msg string, actions ...NotifyAction) *Notification {
	n := &Notification{Level: level, Message: msg, Actions: actions, created: time.Now(), sticky: len(actions) > 0 || level == NotifyError}
	// collapse duplicates
	for _, old := range a.notifications {
		if old.Message == msg {
			old.created = time.Now()
			return old
		}
	}
	a.notifications = append(a.notifications, n)
	if len(a.notifications) > 3 {
		a.notifications = a.notifications[len(a.notifications)-3:]
	}
	a.notifHistory = append(a.notifHistory, n)
	if len(a.notifHistory) > 50 {
		a.notifHistory = a.notifHistory[1:]
	}
	a.unreadNotifications++
	return n
}

func (a *App) expireNotifications() {
	out := a.notifications[:0]
	for _, n := range a.notifications {
		ttl := 8 * time.Second
		if n.sticky {
			ttl = 30 * time.Second
		}
		if time.Since(n.created) < ttl {
			out = append(out, n)
		}
	}
	a.notifications = out
}

func (a *App) toggleNotificationCenter() {
	a.notifCenter = !a.notifCenter
	a.unreadNotifications = 0
}

func (a *App) drawNotifications() {
	th := a.theme
	list := a.notifications
	title := ""
	if a.notifCenter {
		list = a.notifHistory
		title = "NOTIFICATIONS"
		if len(list) == 0 {
			title = "NO NEW NOTIFICATIONS"
		}
	}
	if len(list) == 0 && title == "" {
		return
	}
	w := min(60, a.width-4)
	bottom := a.height - 2
	if a.rStatus.Empty() {
		bottom = a.height - 1
	}
	bgc := th.C("notifications.background")
	fgc := th.C("notifications.foreground")
	base := styleFB(fgc, bgc)
	x := a.width - w - 1
	y := bottom
	// render newest at the bottom
	start := 0
	if a.notifCenter && len(list) > 10 {
		start = len(list) - 10
	}
	for i := len(list) - 1; i >= start; i-- {
		n := list[i]
		lines := wrapText(n.Message, w-6)
		if len(lines) > 6 {
			lines = append(lines[:5], "…")
		}
		h := len(lines)
		if len(n.Actions) > 0 {
			h++
		}
		top := y - h + 1
		if top < 1 {
			break
		}
		r := Rect{x, top, w, h}
		a.cv.Fill(r, ' ', base)
		icon, key := a.Icon(IcoInfo), "notificationsInfoIcon.foreground"
		switch n.Level {
		case NotifyWarning:
			icon, key = a.Icon(IcoWarning), "notificationsWarningIcon.foreground"
		case NotifyError:
			icon, key = a.Icon(IcoError), "notificationsErrorIcon.foreground"
		}
		a.cv.Text(x+1, top, x+3, icon, base.Foreground(th.C(key)))
		for li, l := range lines {
			a.cv.Text(x+3, top+li, x+w-3, l, base)
		}
		a.cv.Text(x+w-2, top, x+w-1, a.Icon(IcoClose), base)
		nn := n
		a.addHit(Rect{x + w - 2, top, 1, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.dismissNotification(nn)
			}
			return true
		})
		if len(n.Actions) > 0 {
			bx := x + w - 1
			for ai := len(n.Actions) - 1; ai >= 0; ai-- {
				act := n.Actions[ai]
				label := " " + act.Label + " "
				bw := StrWidth(label)
				bx -= bw
				st := styleFB(th.C("button.foreground"), th.C("button.background"))
				if ai > 0 {
					st = styleFB(th.C("button.secondaryForeground"), th.C("button.secondaryBackground"))
				}
				a.cv.Text(bx, top+h-1, bx+bw, label, st)
				run := act.Run
				a.addHit(Rect{bx, top + h - 1, bw, 1}, func(ev MouseEvent) bool {
					if ev.Kind == MousePress {
						a.dismissNotification(nn)
						if run != nil {
							run()
						}
					}
					return true
				})
				bx--
			}
		}
		a.addHit(r, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				nn.created = time.Now()
				nn.sticky = true
			}
			return true
		})
		y = top - 1
	}
	if title != "" && y >= 1 {
		hst := styleFB(fgc, th.C("notificationCenterHeader.background"))
		a.cv.Fill(Rect{x, y, w, 1}, ' ', hst)
		a.cv.Text(x+1, y, x+w-4, title, hst)
		a.cv.Text(x+w-4, y, x+w-3, a.Icon(IcoClearAll), hst)
		a.addHit(Rect{x + w - 4, y, 1, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.RunCommand("notifications.clearAll", nil)
			}
			return true
		})
		a.cv.Text(x+w-2, y, x+w-1, a.Icon(IcoChevronDown), hst)
		a.addHit(Rect{x + w - 2, y, 1, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.notifCenter = false
			}
			return true
		})
	}
}

func (a *App) dismissNotification(n *Notification) {
	for i, x := range a.notifications {
		if x == n {
			a.notifications = append(a.notifications[:i], a.notifications[i+1:]...)
			break
		}
	}
	if a.notifCenter {
		for i, x := range a.notifHistory {
			if x == n {
				a.notifHistory = append(a.notifHistory[:i], a.notifHistory[i+1:]...)
				break
			}
		}
	}
}

// setUserSetting writes a user setting and reloads.
func setUserSetting(a *App, key string, value any) error {
	if err := config.SetUser(key, value); err != nil {
		return err
	}
	a.settings = config.LoadSettings(a.root)
	a.applySettingsLight()
	return nil
}

// applySettingsLight re-reads view toggles without reloading themes or keymaps.
func (a *App) applySettingsLight() {
	a.minimapEnabled = a.settings.Bool("editor.minimap.enabled", "")
	a.breadcrumbs = a.settings.Bool("breadcrumbs.enabled", "")
	a.stickyScroll = a.settings.Bool("editor.stickyScroll.enabled", "")
	a.whitespaceRender = a.settings.String("editor.renderWhitespace", "")
	a.lineNumbers = a.settings.String("editor.lineNumbers", "")
}
