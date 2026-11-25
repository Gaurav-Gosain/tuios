package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/previewspec"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// Previewers in quick look: the person's own commands for a type of file
// (session.Previewer). Each previewer that matches the file is a view on the
// row of tabs, before the built-in views, and the first one is the view a
// file opens on. v steps to the next view.
//
// A previewer runs on the machine that has the file: here in this process
// for a file of this machine, there through file-preview-run for a file of
// another. One run is out at a time. Moving to another file or closing the
// preview ends a run here. A run that ended with exit 0 is kept for the
// file, its change time and its size, so going back to a file shows it at
// once. A command that names {width} or {height} runs again when the panel
// changes size.

// quickLookViewRun starts the id of a previewer's view: the prefix and the
// previewer's name.
const quickLookViewRun = "run:"

// quickLookRunKeep bounds the runs kept for files seen before.
const quickLookRunKeep = 8

// quickLookRun is the output of one previewer for one file.
type quickLookRun struct {
	target quickLookTarget
	name   string
	output string
	// lines is the text, or the ANSI text with SGR only, a line each.
	lines []string
	img   *composer.Image
	// note says why there is nothing to show, warn that it is an error.
	note string
	warn bool
	cut  bool
	// w and h are the size the command was given, sized that it named them.
	w, h  int
	sized bool
	// mtime and size are the file's, for the kept runs.
	mtime, size int64
}

// quickLookRunMsg carries a finished run back to the loop.
type quickLookRunMsg struct {
	gen   int
	names []string
	named bool
	run   *quickLookRun
}

// quickLookRunName is the previewer of view v, or "".
func quickLookRunName(v string) string {
	name, _ := strings.CutPrefix(v, quickLookViewRun)
	if name == v {
		return ""
	}
	return name
}

// quickLookRunNames is the previewers that match the file on the panel.
func (ql *quickLookState) quickLookRunNames() []string {
	if r := ql.res; r != nil && r.target == ql.target {
		return r.previewers
	}
	return nil
}

// quickLookRunEnd ends the run that is out, if one is.
func (ql *quickLookState) quickLookRunEnd() {
	if ql.runCancel != nil {
		ql.runCancel()
		ql.runCancel = nil
	}
	ql.runOut = false
	ql.runGen++
}

// quickLookRunSize is the size a previewer is given: the body of the panel.
func (m *OS) quickLookRunSize() (w, h int) {
	innerW, bodyH := m.quickLookSize()
	return max(innerW-3, 1), max(bodyH-3, 1)
}

// quickLookRunRead starts the run the panel needs: the previewer of the view
// on the panel, or a probe of a folder on another machine, whose previewers
// only that machine knows. Update asks after every message.
func (m *OS) quickLookRunRead() tea.Cmd {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || ql.runOut || r == nil || r.target != ql.target || m.previewResizing() {
		return nil
	}
	t := ql.target
	name := quickLookRunName(ql.view)
	probe := name == "" && t.dir && !m.quickLookHere(t) && !ql.runProbed && r.kind == "dir"
	if name == "" && !probe {
		return nil
	}
	w, h := m.quickLookRunSize()
	if run := ql.run; !probe && run != nil && run.target == t && run.name == name && (!run.sized || run.w == w && run.h == h) {
		return nil
	}
	if !probe {
		for _, kept := range ql.runKept {
			if kept.target == t && kept.name == name && kept.mtime == r.mtime && kept.size == r.size && (!kept.sized || kept.w == w && kept.h == h) {
				ql.run = kept
				return nil
			}
		}
	}
	ql.runProbed = ql.runProbed || probe
	line := 1
	if quickLookText(r) && r.firstLine > 0 {
		line = r.firstLine + ql.scroll
	}
	size := previewspec.Size{Width: w, Height: h, Line: line}
	ql.runGen++
	gen := ql.runGen
	ql.runOut = true
	ascii := overlay.UseASCII()
	// Each run goes through file-preview-run on the machine that has the
	// file, this one too. Moving to another file closes the call, and the
	// daemon then ends the run.
	ctx, cancel := context.WithCancel(context.Background())
	ql.runCancel = cancel
	call := m.verbOnHostCallerFor(ctx.Done(), previewspec.TimeoutMax+transferCallTimeout)
	here := m.quickLookHere(t)
	mtime, fsize := r.mtime, r.size
	return func() tea.Msg {
		defer cancel()
		var reply session.PreviewRunReply
		var err error
		if here {
			reply, err = session.PreviewRunHere(ctx, t.path, name, size)
		} else {
			var raw json.RawMessage
			if raw, err = call(t.host, "file-preview-run", map[string]any{"path": t.path, "name": name, "width": size.Width, "height": size.Height, "line": size.Line}); err == nil {
				err = jsonx.Unmarshal(raw, &reply)
			}
		}
		msg := quickLookRunReply(t, name, gen, reply, err, ascii)
		if run := msg.run; run != nil {
			run.w, run.h, run.mtime, run.size = w, h, mtime, fsize
		}
		return msg
	}
}

// quickLookHere reports whether quick look reads t in its own process: a
// file of this machine, with no daemon to ask. With a daemon, the daemon
// reads it, on the machine it runs on.
func (m *OS) quickLookHere(t quickLookTarget) bool {
	return t.host == "" && m.DaemonClient == nil
}

// quickLookRunReply turns a file-preview-run answer for the previewer
// name, or for a probe when name is "", into the message for the panel.
func quickLookRunReply(t quickLookTarget, name string, gen int, reply session.PreviewRunReply, err error, ascii bool) quickLookRunMsg {
	msg := quickLookRunMsg{gen: gen}
	if err != nil {
		msg.run = &quickLookRun{target: t, name: name, note: quickLookErrorText(t, err), warn: true}
		return msg
	}
	msg.names, msg.named = reply.Previewers, true
	switch {
	case reply.Run != nil:
		msg.run = quickLookRunFrom(t, reply.Run, ascii)
		msg.run.sized = reply.Run.Sized
	case name != "":
		msg.run = &quickLookRun{target: t, name: name, note: name + " is not set up on this machine.", warn: true}
	}
	return msg
}

// quickLookRunFrom turns a run into what the panel draws.
func quickLookRunFrom(t quickLookTarget, run *session.PreviewRun, ascii bool) *quickLookRun {
	out := &quickLookRun{target: t, name: run.Name, output: run.Output, cut: run.Cut}
	switch {
	case run.Missing:
		out.note, out.warn = "The program of "+run.Name+" is not on this machine.", true
	case run.TimedOut:
		out.note, out.warn = run.Name+" did not finish in time.", true
	case run.Exit != 0:
		out.note, out.warn = run.Name+" stopped with exit code "+strconv.Itoa(run.Exit)+".", true
		if run.Stderr != "" {
			out.lines = strings.Split(run.Stderr, "\n")
		}
	case run.Cut && run.Output == previewspec.OutputImage:
		out.note, out.warn = "The picture that "+run.Name+" made is too large.", true
	case len(run.Data) == 0:
		out.note = run.Name + " printed nothing."
	case run.Output == previewspec.OutputImage:
		if img, err := composer.NewImage(run.Data); err == nil && img.Width > 0 && img.Height > 0 {
			img.Name = t.name
			out.img = img
		} else {
			out.note, out.warn = "tuios cannot read the picture that "+run.Name+" made.", true
		}
	case run.Output == previewspec.OutputText:
		out.lines, _, _ = quickLookLines("", string(run.Data), ascii)
	default:
		out.lines = strings.Split(strings.TrimRight(quickLookSGROnly(string(run.Data)), "\n"), "\n")
	}
	return out
}

// applyQuickLookRun keeps a run for the file still on the panel.
func (m *OS) applyQuickLookRun(msg quickLookRunMsg) {
	ql := &m.quickLook
	if msg.gen != ql.runGen {
		return
	}
	ql.runOut, ql.runCancel = false, nil
	r := ql.res
	if msg.named && r != nil && r.target == ql.target {
		// A folder of another machine: its previewers came with the run.
		r.previewers = msg.names
		if ql.view == "" && len(msg.names) > 0 {
			ql.quickLookSetView(quickLookViewRun + msg.names[0])
		}
	}
	run := msg.run
	if run == nil {
		return
	}
	if run.name == "" {
		run.name = quickLookRunName(ql.view)
	}
	ql.run = run
	// A folder has no mtime and size here to tell a stale run, so its runs
	// are not kept.
	if run.target.host == "" && !run.target.dir && !run.warn {
		kept := ql.runKept[:0]
		for _, k := range ql.runKept {
			if k.target != run.target || k.name != run.name {
				kept = append(kept, k)
			}
		}
		ql.runKept = append(kept[max(len(kept)+1-quickLookRunKeep, 0):], run)
	}
}

// quickLookRunShown is the run of the view on the panel, when it is read.
func (ql *quickLookState) quickLookRunShown() *quickLookRun {
	name := quickLookRunName(ql.view)
	if run := ql.run; name != "" && run != nil && run.target == ql.target && run.name == name {
		return run
	}
	return nil
}

// quickLookDrawRun draws the view of a previewer.
func (m *OS) quickLookDrawRun(c *ckCanvas, pal overlay.Palette, top, rows, w int) {
	ql := &m.quickLook
	run := ql.quickLookRunShown()
	if run == nil {
		c.text(1, top, w-1, ckSeg{t: "Running " + quickLookRunName(ql.view) + overlay.Ellipsis(), fg: pal.FgMute})
		return
	}
	y := top
	if run.note != "" {
		ink := pal.FgDim
		if run.warn {
			ink = pal.Warn
		}
		c.text(1, y, w-1, ckSeg{t: run.note, fg: ink})
		y, rows = y+2, max(rows-2, 0)
	}
	if run.cut {
		rows = max(rows-1, 0)
		c.text(1, top+rows+(y-top), w-1, ckSeg{t: "The output is too long. This is the start of it.", fg: pal.FgMute})
	}
	ql.viewScroll = max(min(ql.viewScroll, len(run.lines)-rows), 0)
	for i := 0; i < rows && ql.viewScroll+i < len(run.lines); i++ {
		line := run.lines[ql.viewScroll+i]
		if run.output == previewspec.OutputANSI && run.note == "" {
			c.draw(1, y+i, w-2, line)
			continue
		}
		c.text(1, y+i, w-1, ckSeg{t: line, fg: pal.Fg})
	}
}

// quickLookRunPictureRes is the picture of the view on the panel as a result
// the picture code draws, or nil.
func (ql *quickLookState) quickLookRunPictureRes() *quickLookResult {
	run := ql.quickLookRunShown()
	if run == nil || run.img == nil {
		return nil
	}
	return &quickLookResult{target: run.target, kind: "image", img: run.img, width: run.img.Width, height: run.img.Height}
}

// quickLookSGROnly keeps the text and the SGR sequences of s: colours and
// styles. Every other escape and control character is dropped, so the
// output of a command cannot move the cursor, change a mode, set the title
// or pass a sequence through to the terminal. A tab is four spaces.
func quickLookSGROnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var state byte
	for len(s) > 0 {
		seq, width, n, next := ansi.DecodeSequence(s, state, nil)
		r, _ := utf8.DecodeRuneInString(seq)
		switch {
		// A combining mark has no width of its own. A C1 control written
		// as UTF-8 has none either, and some terminals act on it.
		case width > 0 || r >= 0x300 || seq == "\n" || ansi.HasCsiPrefix(seq) && strings.HasSuffix(seq, "m"):
			b.WriteString(seq)
		case seq == "\t":
			b.WriteString("    ")
		}
		state, s = next, s[n:]
	}
	return b.String()
}
