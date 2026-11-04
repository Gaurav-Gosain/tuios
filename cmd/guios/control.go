package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/egoist/mygo"
)

// serveControl answers scripted checks on a unix socket, one command a line,
// in the shape tuios-gpui's control socket uses, so the same scripts measure
// both apps:
//
//	stats          pid, frames drawn, memory, time to the first text
//	dump           the panes as text, the focused one first
//	type TEXT      typed text; \r is Enter
//	key NAME       one key: enter, tab, escape, up, ctrl+c ...
//	action NAME    a tuios action on the focused pane (split_vertical ...)
//	focus ID       focus a pane
//	shot PATH      a PNG of the window
func serveControl(path string, a *guiApp) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("control: %v", err)
		return
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			sc := bufio.NewScanner(conn)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			for sc.Scan() {
				out, _ := json.Marshal(a.control(sc.Text()))
				_, _ = conn.Write(append(out, '\n'))
			}
		}()
	}
}

func (a *guiApp) control(line string) any {
	cmd, arg, _ := strings.Cut(line, " ")
	switch cmd {
	case "stats":
		rss, anon := memoryKB()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		st := map[string]any{"pid": os.Getpid(), "frames": a.frames.Load(), "rss_kb": rss, "anon_kb": anon,
			"heap_inuse_kb": ms.HeapInuse / 1024, "heap_sys_kb": ms.HeapSys / 1024}
		if f := a.first.Load(); f != 0 {
			st["first_text_ms"] = float64(f-a.started.UnixNano()) / 1e6
		}
		return st
	case "resetstats":
		return "ok"
	case "dump":
		return a.dump()
	case "type":
		text := strings.ReplaceAll(arg, `\r`, "\r")
		for i, part := range strings.Split(text, "\r") {
			if i > 0 {
				a.sendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			for _, k := range textKeys(part) {
				a.sendKey(k)
			}
		}
		return "ok"
	case "rawtype":
		// The text straight to the focused pane's PTY, past the model: for
		// timing the model's share of key to echo.
		snap := a.cm.m.Snap()
		for _, p := range snap.Panes {
			if p.ID == snap.Focused {
				_ = p.win.SendInput([]byte(strings.ReplaceAll(arg, `\r`, "\r")))
			}
		}
		return "ok"
	case "key":
		k, ok := namedKey(arg)
		if !ok {
			return "err unknown key"
		}
		a.sendKey(k)
		return "ok"
	case "action":
		done := make(chan string, 1)
		a.cm.do(func(m *model) tea.Cmd {
			cmd, err := m.os.RunActionCmd(arg)
			if err != nil {
				done <- "err " + err.Error()
			} else {
				done <- "ok"
			}
			return cmd
		})
		return wait(done)
	case "focus":
		done := make(chan string, 1)
		a.cm.do(func(m *model) tea.Cmd {
			if err := m.os.FocusPane(arg); err != nil {
				done <- "err " + err.Error()
			} else {
				done <- "ok"
			}
			return nil
		})
		return wait(done)
	case "shot":
		done := make(chan string, 1)
		mygo.RunOnMain(func() {
			png, err := a.win.CapturePage()
			if err == nil {
				err = os.WriteFile(arg, png, 0o644)
			}
			if err != nil {
				done <- "err " + err.Error()
			} else {
				done <- "ok"
			}
		})
		return wait(done)
	}
	return "err unknown command"
}

func wait(ch chan string) string {
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		return "err timeout"
	}
}

type paneDump struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	Text  string `json:"text"`
}

func (a *guiApp) dump() any {
	snap := a.cm.m.Snap()
	if snap == nil {
		return map[string]any{"panes": []paneDump{}}
	}
	var panes []paneDump
	for _, p := range snap.Panes {
		cols, rows, text := paneText(p.win)
		d := paneDump{ID: p.ID, Title: p.Title, Cols: cols, Rows: rows, Text: strings.Join(text, "\n")}
		if p.ID == snap.Focused {
			panes = append([]paneDump{d}, panes...)
		} else {
			panes = append(panes, d)
		}
	}
	return map[string]any{
		"session": snap.Session, "workspace": snap.Workspace, "focused": snap.Focused,
		"cols": snap.Cols, "rows": snap.Rows, "panes": panes,
	}
}

func namedKey(name string) (tea.KeyPressMsg, bool) {
	var mod tea.KeyMod
	parts := strings.Split(strings.ToLower(name), "+")
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			mod |= tea.ModCtrl
		case "alt":
			mod |= tea.ModAlt
		case "shift":
			mod |= tea.ModShift
		}
	}
	last := parts[len(parts)-1]
	named := map[string]rune{"enter": tea.KeyEnter, "tab": tea.KeyTab, "escape": tea.KeyEscape, "esc": tea.KeyEscape,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "backspace": tea.KeyBackspace,
		"space": tea.KeySpace, "pageup": tea.KeyPgUp, "pagedown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd}
	if r, ok := named[last]; ok {
		return tea.KeyPressMsg{Code: r, Mod: mod}, true
	}
	if r := []rune(last); len(r) == 1 {
		k := tea.KeyPressMsg{Code: r[0], Mod: mod}
		if mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			k.Text = last
		}
		return k, true
	}
	return tea.KeyPressMsg{}, false
}

// memoryKB is the process's resident and anonymous memory.
func memoryKB() (rss, anon int) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	for l := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.Atoi(f[1])
		switch f[0] {
		case "VmRSS:":
			rss = n
		case "RssAnon:":
			anon = n
		}
	}
	return rss, anon
}
