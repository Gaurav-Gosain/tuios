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
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	_ "github.com/egoist/mygo/plugins/automation"
	"github.com/egoist/mygo/plugins/terminal"
)

func init() { onNewApp = startProbe }

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
