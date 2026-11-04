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
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/plugins/automation"
	"github.com/egoist/mygo/plugins/terminal"
)

func init() {
	onNewApp = startProbe
	// GUIOS_MOTION_SCALE slows the chrome's animations down, k times, to
	// capture frames on the way.
	if k, err := strconv.ParseFloat(os.Getenv("GUIOS_MOTION_SCALE"), 64); err == nil && k > 0 {
		scaleMotion(k)
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
