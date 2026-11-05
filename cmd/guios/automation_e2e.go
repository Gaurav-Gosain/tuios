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
		panes = append(panes, paneAudit{ID: id, Mode: mode, Chars: n})
	}
	auditFrame = func(a *App, c *ui.Context) {
		for i := range panes {
			b := a.paneRef("card-" + panes[i].ID).Bounds(c)
			panes[i].W, panes[i].H = b.W, b.H
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
					if t, ok := strings.CutPrefix(sc.Text(), "toast "); ok {
						a.update(func() { a.toast(t); a.win.Invalidate() })
						conn.Write([]byte("ok\n"))
						continue
					}
					// "keys" answers the pane whose terminal had the
					// keyboard in the last frame, and whether the scratch
					// dropdown showed: "PANE drop" or "PANE -".
					if sc.Text() == "keys" {
						got := make(chan string, 1)
						a.update(func() {
							k, _ := keysOn.Load().(string)
							d := "-"
							if a.quick.drop {
								d = "drop:" + func() string {
									if p := scratchPane(a.session(a.quick.session)); p != nil {
										return p.ID
									}
									return ""
								}()
							}
							got <- k + " " + d
						})
						conn.Write([]byte(<-got + "\n"))
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
