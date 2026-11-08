package main

import (
	"strconv"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// A one-line prompt over the window, as the TUI's rename and note
// dialogs: Enter keeps what is typed, an empty field clears the name, and
// Escape leaves it as it was.

// promptState is an open prompt.
type promptState struct {
	open        bool
	title       string
	detail      string
	value       string
	placeholder string
	run         func(string)
	// selected tells that the field's text was selected once it took
	// the focus: typing replaces the old name (council round 3, S12).
	selected bool
}

// rename pane, rename workspace and the session's note, as the prefix's
// r, w r and I.

func (a *App) renamePane() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	id := a.focusOf(s)
	p := s.PaneByID(id)
	if p == nil {
		return
	}
	a.prompt = &promptState{open: true, title: locales.PromptRenamePane(), detail: locales.PromptRenamePaneDetail(),
		value: p.PaneName(), placeholder: p.PaneName(),
		run: func(name string) { a.call(id, "pane.rename", proto.RenameParams{ID: id, Name: name}, nil) }}
}

func (a *App) renameWorkspace() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	sid, n := s.ID, s.Workspace
	cur := ""
	for _, w := range s.Workspaces {
		if w.N == n {
			cur = w.Name
		}
	}
	a.prompt = &promptState{open: true, title: locales.PromptRenameWorkspace(itoa(n)), detail: locales.PromptRenameWorkspaceDetail(),
		value: cur, placeholder: strconv.Itoa(n),
		run: func(name string) {
			a.call(sid, "workspace.rename", proto.NameWorkspaceParams{Session: sid, Workspace: n, Name: name}, nil)
		}}
}

func (a *App) setNote() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	sid := s.ID
	a.prompt = &promptState{open: true, title: locales.PromptNote(), detail: locales.PromptNoteDetail(),
		placeholder: locales.PromptNotePlaceholder(),
		run:         func(note string) { a.call(sid, "note", proto.NoteParams{Session: sid, Note: note}, nil) }}
}

// promptView draws the open prompt.
func (a *App) promptView(c *ui.Context, pal *palette) {
	pr := a.prompt
	if pr == nil {
		return
	}
	done := false
	ui.DialogBase(c.Key("prompt"), &pr.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		backdrop.Justify(ui.Start).Padding(120, 0, 0, 0)
		floating(panel.Width(440).Padding(20).Gap(8).Radius(rSheet).Background(pal.popover), pal).Label(pr.title)
		ui.Text(c, pr.title).FontSize(15).FontWeight(650).TextColor(pal.text).SingleLine().Ellipsis("…")
		ui.Text(c, pr.detail).FontSize(12.5).TextColor(pal.textMuted).LineHeight(1.4)
		frame := ui.Row(c.Key("field")).Margin(8, 0, 0, 0)
		var in ui.Element
		frame.Children(func() {
			in = ui.TextInputBase(c.Key("in"), &pr.value).Grow(1).FontSize(14).Placeholder(pr.placeholder).AutoFocus()
			if !pr.selected && in.Focused() {
				pr.selected = true
				in.SetTextSelection(0, utf8.RuneCountInString(pr.value))
			}
			if a.sheetInput("prompt", in) || in.Submitted() {
				done = true
			}
		})
		field(c, frame, in.Focused(), pal)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(12, 0, 0, 0).Children(func() {
			if button(c, "cancel", locales.CommonCancel(), "Esc", false, pal) {
				pr.open = false
			}
			if button(c, "ok", locales.PromptSave(), "↵", true, pal) {
				done = true
			}
		})
	})
	if done {
		pr.open = false
		pr.run(pr.value)
	}
	if !pr.open {
		a.prompt = nil
		a.defer_(a.focusTerminal)
	}
}
