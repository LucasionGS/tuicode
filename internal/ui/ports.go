package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// detectedPort is a TCP port a process started from tuicode listens on.
type detectedPort struct {
	port    int
	pid     int
	process string
}

// PortsView lists ports opened by processes in the integrated terminals, like VS Code's Ports view.
type PortsView struct {
	app      *App
	ports    []detectedPort
	seen     map[int]bool // ports already announced
	busy     bool
	selected int
}

func newPortsView(a *App) *PortsView { return &PortsView{app: a, seen: map[int]bool{}} }

// listeningSockets maps socket inodes to the ports they listen on (from /proc/net/tcp and tcp6).
func listeningSockets() map[string]int {
	out := map[string]int{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		sc.Scan() // header
		for sc.Scan() {
			// Example:
			//   0: 00000000:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 123456 ...
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			port, err := strconv.ParseInt(portHex, 16, 32)
			if err == nil {
				out[fields[9]] = int(port)
			}
		}
		file.Close()
	}
	return out
}

// processTree returns pid and all of its descendants.
func processTree(roots []int) map[int]bool {
	children := map[int][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// the command name is in parentheses and may contain spaces
		s := string(data)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(s[i+1:])
		if len(fields) < 2 {
			continue
		}
		if ppid, err := strconv.Atoi(fields[1]); err == nil {
			children[ppid] = append(children[ppid], pid)
		}
	}
	tree := map[int]bool{}
	var walk func(int)
	walk = func(p int) {
		if tree[p] {
			return
		}
		tree[p] = true
		for _, c := range children[p] {
			walk(c)
		}
	}
	for _, r := range roots {
		if r > 0 {
			walk(r)
		}
	}
	return tree
}

// detectPorts finds listening ports owned by the given process trees.
func detectPorts(roots []int) []detectedPort {
	sockets := listeningSockets()
	if len(sockets) == 0 {
		return nil
	}
	var out []detectedPort
	seen := map[int]bool{}
	for pid := range processTree(roots) {
		fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
			if port, ok := sockets[inode]; ok && !seen[port] {
				seen[port] = true
				name, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
				out = append(out, detectedPort{port: port, pid: pid, process: strings.TrimSpace(string(name))})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].port < out[j].port })
	return out
}

// refresh rescans ports in the background and announces new ones.
func (p *PortsView) refresh() {
	a := p.app
	if p.busy || len(a.terminals.terms) == 0 {
		if len(a.terminals.terms) == 0 {
			p.ports = nil
		}
		return
	}
	var roots []int
	for _, ti := range a.terminals.terms {
		roots = append(roots, ti.t.Pid())
	}
	p.busy = true
	go func() {
		ports := detectPorts(roots)
		a.Post(func() {
			p.busy = false
			p.ports = ports
			for _, dp := range ports {
				if p.seen[dp.port] {
					continue
				}
				p.seen[dp.port] = true
				if a.settings.Bool("remote.autoForwardPorts", "") {
					port := dp.port
					a.notify(NotifyInfo, fmt.Sprintf("Your application running on port %d is available.", port),
						NotifyAction{Label: "Open in Browser", Run: func() { openExternal(fmt.Sprintf("http://localhost:%d", port)) }})
				}
			}
		})
	}()
}

func (p *PortsView) draw(r Rect) {
	a := p.app
	th := a.theme
	bgc := th.C("panel.background")
	desc := styleFB(th.C("descriptionForeground"), bgc)
	if len(p.ports) == 0 {
		a.cv.Text(r.X+2, r.Y+1, r.X+r.W, "No ports detected. Ports opened by programs in the terminal show up here.", desc)
		return
	}
	cols := []int{r.X + 2, r.X + 14, r.X + 42}
	hst := styleFB(th.C("foreground"), bgc).Bold(true)
	a.cv.Text(cols[0], r.Y, r.X+r.W, "Port", hst)
	a.cv.Text(cols[1], r.Y, r.X+r.W, "Forwarded Address", hst)
	a.cv.Text(cols[2], r.Y, r.X+r.W, "Running Process", hst)
	focused := a.focus == FocusPanel && a.panelView == "ports"
	p.selected = clampInt(p.selected, 0, len(p.ports)-1)
	for i, dp := range p.ports {
		y := r.Y + 1 + i
		if y >= r.Y+r.H {
			break
		}
		rbg := bgc
		if i == p.selected && focused {
			rbg = th.C("list.activeSelectionBackground")
		} else if a.mouseY == y && r.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(th.C("foreground"), rbg)
		a.cv.Fill(Rect{r.X, y, r.W, 1}, ' ', st)
		a.cv.Text(cols[0], y, cols[1]-1, a.Icon(IcoServer)+" "+strconv.Itoa(dp.port), st)
		addr := fmt.Sprintf("localhost:%d", dp.port)
		a.cv.Text(cols[1], y, cols[2]-1, addr, st.Foreground(th.C("textLink.foreground")))
		a.cv.Text(cols[2], y, r.X+r.W, fmt.Sprintf("%s (%d)", dp.process, dp.pid), st.Foreground(th.OverColor("descriptionForeground", rbg)))
		port := dp.port
		idx := i
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			if ev.Kind != MousePress {
				return true
			}
			p.selected = idx
			a.setFocus(FocusPanel)
			if ev.Button == tcell.Button2 {
				a.openContextMenu(ev.X, ev.Y, p.menu(port))
			} else if ev.X >= cols[1] && ev.X < cols[1]+len(addr) || ev.Clicks >= 2 {
				openExternal(fmt.Sprintf("http://localhost:%d", port))
			}
			return true
		})
	}
}

func (p *PortsView) menu(port int) []MenuItem {
	a := p.app
	addr := fmt.Sprintf("localhost:%d", port)
	return []MenuItem{
		{Label: "Open in Browser", Run: func() { openExternal("http://" + addr) }},
		{Label: "Copy Local Address", Run: func() { a.setClipboard(addr) }},
	}
}

func (p *PortsView) handleKey(key string) bool {
	switch key {
	case "up":
		p.selected = max(p.selected-1, 0)
	case "down":
		p.selected = min(p.selected+1, max(len(p.ports)-1, 0))
	case "enter":
		if p.selected < len(p.ports) {
			openExternal(fmt.Sprintf("http://localhost:%d", p.ports[p.selected].port))
		}
	case "ctrl+c":
		if p.selected < len(p.ports) {
			p.app.setClipboard(fmt.Sprintf("localhost:%d", p.ports[p.selected].port))
		}
	case "escape":
		p.app.focusEditor()
	default:
		return false
	}
	return true
}
