package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/tape"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The tape manager (the prefix and T): the tapes in tuios's tape folder,
// as the TUI lists them, to play one. With a TUI on the session the TUI
// plays it (tuios tape exec), every command. With none, guios plays the
// commands that type into the pane, wait, and make, close and move between
// panes, and says how many others it left out.

// tapeDir is the folder tuios keeps tapes in (internal/app's
// GetTapeDirectory).
func tapeDir() string {
	p, err := xdg.DataFile("tuios/tapes")
	if err != nil {
		return ""
	}
	return filepath.Dir(p)
}

// openTapes lists the tapes in the palette.
func (a *App) openTapes() {
	dir := tapeDir()
	entries, _ := os.ReadDir(dir)
	var items []paletteItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tape") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		detail := ""
		if info, err := e.Info(); err == nil {
			detail = ago(info.ModTime().UnixMilli(), time.Now())
		}
		items = append(items, paletteItem{title: strings.TrimSuffix(e.Name(), ".tape"), detail: detail, words: "tape",
			run: func() { a.playTape(path) }})
	}
	if len(items) == 0 {
		a.toast(locales.KeysNoTapes(dir))
		return
	}
	a.openPalette()
	a.pal.custom, a.pal.customLabel = items, locales.KeysTapesLabel()
}

// playTape plays a tape on the session shown.
func (a *App) playTape(path string) {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	name := filepath.Base(path)
	if s.TUI && !a.hostOf(s.ID).remote() {
		sid := s.ID
		go func() {
			out, err := exec.Command(cli.Tuios, "tape", "exec", "--session", a.sessionName(sid), path).CombinedOutput()
			a.update(func() {
				if err != nil {
					a.toast(strings.TrimSpace(string(out)))
					return
				}
				a.toast(locales.KeysTapePlayed(name))
			})
		}()
		return
	}
	script, err := tape.LoadFile(path)
	if err != nil {
		a.toast(err.Error())
		return
	}
	if len(script.Errors) > 0 {
		a.toast(script.Errors[0])
		return
	}
	cmds := slices.Clone(script.Commands)
	a.toast(locales.KeysTapePlaying(name))
	go func() {
		skipped := 0
		for i := range cmds {
			if !a.tapeStep(&cmds[i]) {
				skipped++
			}
			if d := cmds[i].Delay; d > 0 {
				time.Sleep(d)
			}
		}
		a.update(func() {
			if skipped > 0 {
				a.toast(locales.KeysTapeSkipped(name, strconv.Itoa(skipped)))
				return
			}
			a.toast(locales.KeysTapePlayed(name))
		})
	}()
}

// sessionName is the daemon's name of a session.
func (a *App) sessionName(sid string) string {
	if h := a.hostOf(sid); h.cl != nil {
		return h.cl.SessionName(sid)
	}
	return sid
}

// tapeKeys are the bytes of the tape's key commands.
var tapeKeys = map[tape.CommandType]string{
	tape.CommandTypeEnter: "\r", tape.CommandTypeSpace: " ", tape.CommandTypeBackspace: "\x7f",
	tape.CommandTypeDelete: "\x1b[3~", tape.CommandTypeTab: "\t", tape.CommandTypeEscape: "\x1b",
	tape.CommandTypeUp: "\x1b[A", tape.CommandTypeDown: "\x1b[B", tape.CommandTypeRight: "\x1b[C",
	tape.CommandTypeLeft: "\x1b[D", tape.CommandTypeHome: "\x1b[H", tape.CommandTypeEnd: "\x1b[F",
}

// tapeStep runs one command off the main thread. It reports false for a
// command guios does not play.
func (a *App) tapeStep(c *tape.Command) bool {
	send := func(b string) {
		done := make(chan struct{})
		a.update(func() {
			defer close(done)
			if s := a.session(a.selected); s != nil {
				if t := a.termFor(a.focusOf(s)); t != nil {
					t.Send([]byte(b))
				}
			}
		})
		<-done
	}
	act := func(fn func(s *proto.Session)) {
		done := make(chan struct{})
		a.update(func() {
			defer close(done)
			if s := a.session(a.selected); s != nil {
				fn(s)
			}
		})
		<-done
		time.Sleep(300 * time.Millisecond) // the new pane attaches
	}
	switch c.Type {
	case tape.CommandTypeType:
		send(strings.Join(c.Args, " "))
	case tape.CommandTypeSleep:
		if len(c.Args) > 0 {
			if d, err := tape.ParseDuration(c.Args[0]); err == nil {
				time.Sleep(d)
			}
		}
	case tape.CommandTypeKeyCombo:
		if len(c.Args) == 0 {
			return false
		}
		kc, err := tape.ParseKeyCombo(c.Args[0])
		if err != nil {
			return false
		}
		k := strings.ToLower(kc.Key)
		if kc.Alt {
			k = "alt+" + k
		}
		if kc.Ctrl {
			k = "ctrl+" + k
		}
		b := keyBytes(k)
		if b == nil {
			return false
		}
		send(string(b))
	case tape.CommandTypeNewWindow, tape.CommandTypeSplit:
		act(func(s *proto.Session) { a.splitPane(s, "", "right") })
	case tape.CommandTypeCloseWindow:
		act(func(*proto.Session) { a.closeFocused() })
	case tape.CommandTypeNextWindow:
		act(func(*proto.Session) { a.cyclePane(1) })
	case tape.CommandTypePrevWindow:
		act(func(*proto.Session) { a.cyclePane(-1) })
	case tape.CommandTypeToggleZoom:
		act(func(s *proto.Session) { a.zoomPane(s, "") })
	case tape.CommandTypeTerminalMode, tape.CommandTypeWindowManagementMode, tape.CommandTypeComment,
		tape.CommandTypeEnableAnimations, tape.CommandTypeDisableAnimations, tape.CommandTypeToggleAnimations:
	default:
		if b, ok := tapeKeys[c.Type]; ok {
			n := 1
			if len(c.Args) > 0 {
				if v, err := strconv.Atoi(c.Args[0]); err == nil && v > 0 {
					n = min(v, 1000)
				}
			}
			send(strings.Repeat(b, n))
			return true
		}
		return false
	}
	return true
}
