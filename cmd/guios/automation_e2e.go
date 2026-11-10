//go:build guiose2e

package main

// The automation socket for scripted checks and frames: MYGO_AUTOMATION
// names it. Only a build with -tags guiose2e has it.
//
// GUIOS_PROBE names a second socket for latency checks: each line "count
// C" is answered with how many times the character C appears in the text
// of the terminals the app holds. It reads the emulators directly, off the
// main thread, so a key sent through the automation socket can be timed to
// its echo in the emulator.
import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/mygo"
	_ "github.com/Gaurav-Gosain/mygo/plugins/automation"
	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"
)

func init() {
	onNewApp = startProbe
	startAudit()
	// GUIOS_MOTION_SCALE slows the chrome's animations down, k times, to
	// capture frames on the way.
	if k, err := strconv.ParseFloat(os.Getenv("GUIOS_MOTION_SCALE"), 64); err == nil && k > 0 {
		scaleMotion(k)
	}
	// GUIOS_ATTACH_DELAY holds every pane's attach that long, to check
	// what a person types meanwhile (typeahead.go).
	if d, err := time.ParseDuration(os.Getenv("GUIOS_ATTACH_DELAY")); err == nil {
		attachDelay = d
	}
	// GUIOS_ACTION_DELAY runs every prefix action that long after the
	// prefix took its key: a main thread slow to come back, which a window
	// out of sight gives now and then, so what is typed meanwhile shows
	// where it goes (tuios_keys.go).
	if d, err := time.ParseDuration(os.Getenv("GUIOS_ACTION_DELAY")); err == nil && d > 0 {
		postAction = func(a *App, fn func()) {
			time.AfterFunc(d, func() { a.update(fn) })
		}
	}
	// GUIOS_KEY_AUDIT=FILE writes a line for each thing the keyboard
	// owner does (key_owner.go): hold, keep, done, land and drop, with the
	// time, so a key lost or sent to the wrong place shows where.
	if path := os.Getenv("GUIOS_KEY_AUDIT"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			keyAudit = func(ev, what, note string) {
				fmt.Fprintf(f, "%s %s %q %s\n", time.Now().Format("15:04:05.000"), ev, what, note)
			}
			auditCaller = func() string {
				var out []string
				for i := 3; i < 6; i++ {
					if _, file, line, ok := runtime.Caller(i); ok {
						out = append(out, filepath.Base(file)+":"+strconv.Itoa(line))
					}
				}
				return strings.Join(out, " ")
			}
		}
	}
	// GUIOS_SCROLL_TRACE prints where smoothScroll puts the content in
	// each frame, as MyGo's MYGO_SCROLL_TRACE does for a terminal.
	if os.Getenv("GUIOS_SCROLL_TRACE") != "" {
		scrollTrace = func(y float32) { fmt.Fprintf(os.Stderr, "SCROLLTRACE %d %.2f\n", time.Now().UnixNano(), y) }
	}
}

// startAudit writes, with GUIOS_FRAME_AUDIT=FILE, one JSON line a frame:
// its time and, for each pane the frame draws, how its terminal shows
// (live, copy or blank), the card's size, and how many characters other
// than spaces its terminal holds.
func startAudit() {
	path := os.Getenv("GUIOS_FRAME_AUDIT")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	type paneAudit struct {
		ID    string  `json:"id"`
		Mode  string  `json:"mode"`
		W     float32 `json:"w"`
		H     float32 `json:"h"`
		Chars int     `json:"chars"`
		// Cols is the terminal's width in cells, and TW the width of the
		// view that draws it: a change of theme must change neither.
		Cols int     `json:"cols"`
		TW   float32 `json:"tw"`
		X    float32 `json:"x"`
	}
	var panes []paneAudit
	auditPane = func(id, mode string, t *terminal.Terminal) {
		n := 0
		if t != nil {
			for _, r := range t.Text() {
				if r != ' ' && r != '\n' {
					n++
				}
			}
		}
		cols := 0
		if t != nil {
			cols, _ = t.Size()
		}
		panes = append(panes, paneAudit{ID: id, Mode: mode, Chars: n, Cols: cols})
	}
	auditFrame = func(a *App, c *ui.Context) {
		for i := range panes {
			b := a.paneRef("card-" + panes[i].ID).Bounds(c)
			panes[i].W, panes[i].H, panes[i].X = b.W, b.H, b.X
			panes[i].TW = a.paneRef(panes[i].ID).Bounds(c).W
		}
		line, _ := json.Marshal(map[string]any{"t": c.Now().UnixNano(), "panes": panes})
		w.Write(append(line, '\n'))
		w.Flush()
		panes = panes[:0]
	}
}

func startProbe(a *App) {
	path := os.Getenv("GUIOS_PROBE")
	if path == "" {
		return
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return
	}
	var mu sync.Mutex
	terms := func() []*terminal.Terminal {
		done := make(chan []*terminal.Terminal, 1)
		a.update(func() {
			var ts []*terminal.Terminal
			for _, e := range a.terms {
				if e.term != nil {
					ts = append(ts, e.term)
				}
			}
			done <- ts
		})
		return <-done
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				mu.Lock()
				ts := terms()
				mu.Unlock()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					// "image PATH" puts an image in the reply box of the
					// cockpit's agent, as a paste of it does: a headless
					// compositor has no clipboard to paste from.
					if p, ok := strings.CutPrefix(sc.Text(), "image "); ok {
						a.update(func() {
							if s := a.ck.spawn; s != nil && s.open {
								a.addImageFiles(&s.task, []string{p})
								return
							}
							if ag, ok := a.ckSelectedAgent(); ok {
								a.addImageFiles(a.draftFor(ag), []string{p})
							}
						})
						conn.Write([]byte("ok\n"))
						continue
					}
					// "confirm" opens a dialog no key asked for, as an event
					// of the daemon does: what the keys kept then do (G2).
					if sc.Text() == "confirm" {
						a.update(func() {
							a.confirm = &confirmation{open: true, title: "Probe", detail: "A dialog the probe opened.", action: "OK", run: func() {}}
						})
						conn.Write([]byte("ok\n"))
						continue
					}
					// "size W H" sizes the main window, for frames at a
					// width: a headless compositor has no edge to drag.
					if p, ok := strings.CutPrefix(sc.Text(), "size "); ok {
						var w, h int
						if _, err := fmt.Sscan(p, &w, &h); err == nil {
							a.update(func() { a.win.SetSize(w, h) })
						}
						conn.Write([]byte("ok\n"))
						continue
					}
					// "clip" answers the clipboard's text, read through the
					// app as a paste reads it, and "clip TEXT" writes it.
					if sc.Text() == "clip" || strings.HasPrefix(sc.Text(), "clip ") {
						set, write := strings.CutPrefix(sc.Text(), "clip ")
						got := make(chan string, 1)
						a.update(func() {
							if write {
								mygo.Clipboard.WriteText(set)
							}
							got <- mygo.Clipboard.ReadText()
						})
						conn.Write([]byte(strconv.Quote(<-got) + "\n"))
						continue
					}
					// "toast TEXT" shows a toast, for frames of one coming
					// and going.
					// "action NAME" runs a keymap action as its key would,
					// and answers the toast it left after a second and a
					// half ("" for none): the check that a bound name works
					// in guios rather than saying it needs the TUI. A
					// run-command answers on the daemon's time, so the wait.
					if name, ok := strings.CutPrefix(sc.Text(), "action "); ok {
						a.update(func() { a.lastToast = ""; a.runKeyAction(name) })
						time.Sleep(1500 * time.Millisecond)
						got := make(chan string, 1)
						a.update(func() { got <- a.lastToast })
						line, _ := json.Marshal(map[string]string{"action": name, "toast": <-got})
						conn.Write(append(line, '\n'))
						continue
					}
					// "opencheck" proves the opener writes to GUIOS_OPEN_LOG
					// instead of opening: the QA kit runs it before any probe
					// and refuses to go on when it answers "unguarded". With
					// no log it opens nothing at all.
					if sc.Text() == "opencheck" {
						if openLog == "" {
							conn.Write([]byte("unguarded\n"))
							continue
						}
						done := make(chan struct{})
						a.update(func() { a.openExternal("guios-open-check://probe"); close(done) })
						<-done
						conn.Write([]byte("logged\n"))
						continue
					}
					// "hints" opens hint mode on the focused pane, as the
					// prefix and F do, and answers each label's text, as one
					// JSON line. "hintkey K" gives hint mode one key, as the
					// keymap spells it ("a", "A", "ctrl+a", "super+a").
					if sc.Text() == "hints" {
						got := make(chan map[string]string, 1)
						a.update(func() {
							a.openHints()
							m := map[string]string{}
							if a.cm != nil {
								for _, h := range a.cm.hints {
									m[h.label] = h.text
								}
							}
							got <- m
						})
						line, _ := json.Marshal(<-got)
						conn.Write(append(line, '\n'))
						continue
					}
					if k, ok := strings.CutPrefix(sc.Text(), "hintkey "); ok {
						done := make(chan struct{})
						a.update(func() { a.lastToast = ""; a.copyKey(k); close(done) })
						<-done
						time.Sleep(1500 * time.Millisecond)
						got := make(chan string, 1)
						a.update(func() { got <- a.lastToast })
						line, _ := json.Marshal(map[string]string{"key": k, "toast": <-got})
						conn.Write(append(line, '\n'))
						continue
					}
					// "loadkeys" reads the tuios keymap again, as the window's
					// focus does: a QA run changes the config while the window
					// has no focus to give. "stepkey K" gives the prefix one key,
					// as the keymap spells it, as a press does, and answers
					// whether the prefix took it, whether it is still armed, the
					// menu it is in, and the toast after a second and a half.
					// "arm" arms the prefix on the focused pane, as the leader
					// key does.
					if sc.Text() == "arm" {
						done := make(chan struct{})
						a.update(func() {
							pane := ""
							if s := a.session(a.selected); s != nil {
								pane = a.focusOf(s)
							}
							a.arm(pane)
							close(done)
						})
						<-done
						conn.Write([]byte("ok\n"))
						continue
					}
					// "palette Q" opens the palette (or keeps the list it
					// shows, a launcher's say) with the query Q and answers the
					// ranked rows, title and detail, the first 30. "palettepick
					// N" runs row N of that ranking as Enter does: the palette
					// closes, then the row runs.
					if q, ok := strings.CutPrefix(sc.Text(), "palette"); ok && (q == "" || q[0] == ' ') {
						got := make(chan []map[string]string, 1)
						a.update(func() {
							if !a.pal.open {
								a.openPalette()
							}
							a.pal.query = strings.TrimSpace(q)
							var rows []map[string]string
							for i, it := range a.paletteRanked() {
								if i == 30 {
									break
								}
								rows = append(rows, map[string]string{"title": it.title, "detail": it.detail})
							}
							got <- rows
						})
						line, _ := json.Marshal(<-got)
						conn.Write(append(line, '\n'))
						continue
					}
					if n, ok := strings.CutPrefix(sc.Text(), "palettepick "); ok {
						done := make(chan string, 1)
						a.update(func() {
							ranked := a.paletteRanked()
							i, err := strconv.Atoi(n)
							if err != nil || i < 0 || i >= len(ranked) {
								done <- "no such row"
								return
							}
							a.pal.open = false
							ranked[i].run()
							done <- ranked[i].title
						})
						conn.Write([]byte(<-done + "\n"))
						continue
					}
					// "helprows" answers the help sheet's rows: group, keys,
					// what.
					if sc.Text() == "helprows" {
						got := make(chan [][3]string, 1)
						a.update(func() {
							var rows [][3]string
							for _, r := range a.helpRows() {
								rows = append(rows, [3]string{r.group, r.keys, r.what})
							}
							got <- rows
						})
						line, _ := json.Marshal(<-got)
						conn.Write(append(line, '\n'))
						continue
					}
					if sc.Text() == "loadkeys" {
						done := make(chan struct{})
						a.update(func() { a.loadKeys(); close(done) })
						<-done
						conn.Write([]byte("ok\n"))
						continue
					}
					if k, ok := strings.CutPrefix(sc.Text(), "stepkey "); ok {
						took := make(chan bool, 1)
						a.update(func() { a.lastToast = ""; took <- a.stepKey(k) })
						t := <-took
						time.Sleep(1500 * time.Millisecond)
						got := make(chan map[string]any, 1)
						a.update(func() {
							a.tk.mu.Lock()
							menu := a.tk.menu
							a.tk.mu.Unlock()
							got <- map[string]any{"key": k, "took": t, "armed": a.tk.armed.Load(), "menu": menu, "toast": a.lastToast}
						})
						line, _ := json.Marshal(<-got)
						conn.Write(append(line, '\n'))
						continue
					}
					if t, ok := strings.CutPrefix(sc.Text(), "toast "); ok {
						a.update(func() { a.toast(t); a.win.Invalidate() })
						conn.Write([]byte("ok\n"))
						continue
					}
					// "texts" answers each pane's terminal text, by pane
					// id, as one JSON line: the QA's check that a pane
					// shows the daemon's screen.
					if sc.Text() == "texts" {
						got := make(chan map[string]string, 1)
						a.update(func() {
							m := map[string]string{}
							for id, e := range a.terms {
								if e.term != nil {
									m[id] = e.term.Text()
								}
							}
							got <- m
						})
						line, _ := json.Marshal(<-got)
						conn.Write(append(line, '\n'))
						continue
					}
					c, ok := strings.CutPrefix(sc.Text(), "count ")
					if !ok {
						continue
					}
					n := 0
					for _, t := range ts {
						n += strings.Count(t.Text(), c)
					}
					conn.Write([]byte(strconv.Itoa(n) + "\n"))
				}
			}()
		}
	}()
}

// scaleMotion makes every animation of motion.go k times as long: the e2e
// build's GUIOS_MOTION_SCALE slows them down to capture frames on the way.
func scaleMotion(k float64) {
	for _, a := range []*ui.Animation{&motionQuick, &motionOut, &motionBase, &motionLayout, &sidebarMotion} {
		a.Duration = time.Duration(float64(a.Duration) * k)
	}
	enterKeys = []ui.Keyframe{{At: 0, Value: 0}, {At: motionQuick.Duration, Value: 1, Ease: easeOut}}
}
