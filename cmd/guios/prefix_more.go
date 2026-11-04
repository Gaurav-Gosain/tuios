package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/mygo"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The tuios prefix keys guios runs that are no action of kero's: each does
// what the TUI's key does (internal/input), on guios's own screen.

// verbOn calls a daemon verb on a session of the selected session's host,
// and hands its JSON to done on the main thread.
func (a *App) verbOn(sid, verb string, args map[string]any, done func(raw json.RawMessage, err error)) {
	h := a.hostOf(sid)
	if h.cl == nil {
		return
	}
	cl := h.cl
	go func() {
		var raw json.RawMessage
		err := cl.Call("verb", proto.VerbParams{Verb: verb, Session: sid, Args: args}, &raw)
		if done != nil {
			a.update(func() { done(raw, err) })
		} else if err != nil {
			a.update(func() { a.toast(err.Error()) })
		}
	}()
}

// paneRef is a pane of a session, for the moves between panes.
type panePlace struct {
	sid  string
	pane *proto.Pane
}

// everyPane is every pane of every session, in the sidebar's order: the
// shown workspace's first, then the others'.
func (a *App) everyPane() []panePlace {
	var out []panePlace
	for _, id := range a.visibleOrder() {
		s := a.session(id)
		if s == nil {
			continue
		}
		for i := range s.Panes {
			out = append(out, panePlace{s.ID, &s.Panes[i]})
		}
		for i := range s.Others {
			out = append(out, panePlace{s.ID, &s.Others[i]})
		}
	}
	return out
}

// stepAgent goes to the next or the previous pane an agent runs in, in
// the sidebar's order, round past the end: the prefix and J, K.
func (a *App) stepAgent(d int) {
	var agents []panePlace
	cur := -1
	inPane := ""
	if s := a.session(a.selected); s != nil {
		inPane = a.focusOf(s)
	}
	for _, p := range a.everyPane() {
		if p.pane.Agent == "" {
			continue
		}
		if p.sid == a.selected && p.pane.ID == inPane {
			cur = len(agents)
		}
		agents = append(agents, p)
	}
	if len(agents) == 0 {
		a.toast(locales.KeysNoAgents())
		return
	}
	i := (cur + d + len(agents)) % len(agents)
	if cur < 0 && d < 0 {
		i = len(agents) - 1
	}
	a.jumpToPane(agents[i].sid, agents[i].pane.ID)
}

// newestPane is the pane whose state changed last among those want takes.
func (a *App) newestPane(want func(*proto.Pane) bool) (panePlace, bool) {
	var best panePlace
	found := false
	for _, p := range a.everyPane() {
		if want(p.pane) && (!found || p.pane.Since > best.pane.Since) {
			best, found = p, true
		}
	}
	return best, found
}

// jumpNewestFinished goes to the newest turn an agent finished that
// nobody has seen: the prefix and O.
func (a *App) jumpNewestFinished() {
	p, ok := a.newestPane(func(p *proto.Pane) bool { return p.Unread && p.Status == proto.Idle })
	if !ok {
		a.toast(locales.KeysNothingFinished())
		return
	}
	a.jumpToPane(p.sid, p.pane.ID)
}

// jumpNewestMessage goes to the pane with the newest message, a program's
// notification or report: the prefix and j. showLast shows it in full
// instead: the prefix and N.
func (a *App) jumpNewestMessage(showLast bool) {
	p, ok := a.newestPane(func(p *proto.Pane) bool { return p.Message != "" })
	if !ok {
		a.toast(locales.KeysNoMessage())
		return
	}
	if showLast {
		name := p.pane.PaneName()
		if s := a.session(p.sid); s != nil {
			name = s.DisplayName(a.set.SessionName) + " · " + name
		}
		a.toast(name + ": " + p.pane.Message)
		return
	}
	a.jumpToPane(p.sid, p.pane.ID)
}

// pasteClipboardImage pastes the clipboard's image as the path of a file
// with it, as an agent takes an image: the prefix and V.
func (a *App) pasteClipboardImage() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	id := a.focusOf(s)
	d, err := mygo.Clipboard.Read()
	if err != nil || !a.pasteImage(id, d) {
		a.toast(locales.KeysNoImage())
	}
}

// pasteBuffer types the newest paste buffer into the pane: the prefix and
// ]. A copy mode yank adds one.
func (a *App) pasteBuffer(name string) {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	args := map[string]any{"window": a.focusOf(s)}
	if name != "" {
		args["name"] = name
	}
	a.verbOn(s.ID, "paste-buffer", args, func(_ json.RawMessage, err error) {
		if err != nil {
			a.toast(locales.KeysNoBuffer())
		}
	})
}

// chooseBuffer lists the paste buffers to paste one: the prefix and #.
func (a *App) chooseBuffer() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	a.verbOn(s.ID, "list-buffers", nil, func(raw json.RawMessage, err error) {
		var res struct {
			Buffers []struct {
				Name   string `json:"name"`
				Sample string `json:"sample"`
				Bytes  int    `json:"bytes"`
			} `json:"buffers"`
		}
		if err != nil || json.Unmarshal(raw, &res) != nil || len(res.Buffers) == 0 {
			a.toast(locales.KeysNoBuffer())
			return
		}
		var items []paletteItem
		for _, b := range res.Buffers {
			name := b.Name
			title := strings.Join(strings.Fields(b.Sample), " ")
			if title == "" {
				title = name
			}
			items = append(items, paletteItem{title: title, detail: name + " · " + itoa(b.Bytes) + " B", run: func() { a.pasteBuffer(name) }})
		}
		a.openPalette()
		a.pal.custom, a.pal.customLabel = items, locales.KeysBuffersLabel()
	})
}

// addBuffer keeps a copy mode yank as a paste buffer, as the TUI's does.
func (a *App) addBuffer(text string) {
	if s := a.session(a.selected); s != nil && text != "" {
		a.verbOn(s.ID, "set-buffer", map[string]any{"data": text}, func(json.RawMessage, error) {})
	}
}

// screenshot renders the pane to a picture under screenshot.directory:
// the prefix and C.
func (a *App) screenshot() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	a.verbOn(s.ID, "screenshot", map[string]any{"window": a.focusOf(s)}, func(raw json.RawMessage, err error) {
		var res struct {
			Path string `json:"path"`
		}
		if err != nil {
			a.toast(err.Error())
			return
		}
		_ = json.Unmarshal(raw, &res)
		a.toast(locales.KeysScreenshotSaved(res.Path))
	})
}

// pickPath asks for a file or folder and types its path at the pane's
// cursor, quoted for the shell: the prefix and .
func (a *App) pickPath() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	id := a.focusOf(s)
	dir := s.WorkDir()
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: locales.KeysPickPathTitle(), Directory: false,
			ButtonLabel: locales.CommonOpen(), DefaultPath: dir})
		if err != nil || len(paths) == 0 {
			return
		}
		a.update(func() {
			if t := a.termFor(id); t != nil {
				t.Send([]byte(shellQuote(paths[0])))
			}
			a.focusTerminal()
		})
	}()
}

// shellQuote quotes a path for a POSIX shell when it needs it.
func shellQuote(p string) string {
	if p != "" && strings.IndexFunc(p, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:@%~,", r))
	}) < 0 {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// editScrollback opens the pane's history in the editor ($VISUAL, else
// $EDITOR, else vi), in a pane beside it: the prefix and E.
func (a *App) editScrollback() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	id := a.focusOf(s)
	t := a.termFor(id)
	if t == nil {
		return
	}
	f, err := os.CreateTemp("", "guios-scrollback-*.txt")
	if err != nil {
		a.toast(err.Error())
		return
	}
	_, _ = f.WriteString(t.Text())
	_ = f.Close()
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	a.split(s, proto.CreateParams{Split: id, Direction: "right", Command: []string{"sh", "-c", ed + ` "$1"; rm -f "$1"`, "sh", filepath.Clean(f.Name())}})
}
