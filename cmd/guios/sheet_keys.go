package main

import (
	"slices"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// The keys a sheet takes from the owner. A prefix action that opens a
// sheet (the palette, a rename, the help, a new worktree) runs a turn of
// the main thread after its key, and the sheet's field takes the focus a
// frame after that. What is typed meanwhile is the sheet's: text meant for
// the palette that reached a shell could run there as a command. The
// owner keeps those keys (actStart), and once the sheet has the keyboard
// writes them into its field, in order (replaySheet). A key the sheet has
// no use for drops, with a line in the key audit: it never goes on to a
// pane (design-G, the lead's go).

// sheetField is the field of the sheet open now, as the sheet's last build
// bound it (sheetInput).
type sheetField struct {
	name  string // sheetName of the sheet that bound it
	pass  int    // the owner's pass of that build
	ref   ui.Handle
	toEnd bool // the caret goes to the end: the owner wrote text
	enter bool // a kept Enter: the sheet submits
	// The confirm dialog has no field: its Cancel button, which has the
	// focus, is bound here, by the build of pass cpass. answer is a kept
	// Enter or Escape the dialog reads in its next build (replayConfirm).
	cancel ui.Handle
	cpass  int
	answer ui.Key
}

// sheetName names the open sheet whose field the owner writes, "" for
// none or for one it does not write.
func (a *App) sheetName() string {
	switch {
	case a.confirm != nil && a.confirm.open:
		return "confirm"
	case a.prompt != nil && a.prompt.open:
		return "prompt"
	case a.wtAsk != nil && a.wtAsk.open:
		return "branch"
	case a.pal.open:
		return "palette"
	case a.help.open:
		return "help"
	}
	return ""
}

// sheetInput binds a sheet's field for the owner, as its build makes it,
// and reports whether a kept Enter submits it.
func (a *App) sheetInput(name string, in ui.Element) bool {
	if a.sheetName() != name {
		// Two sheets open at once, as a rename run over the help: only
		// the one that owns the keys binds the shared field, since two
		// bound in one build ended the app.
		return false
	}
	sf := &a.sf
	in.Bind(&sf.ref)
	sf.name, sf.pass = name, a.ko.pass
	if sf.toEnd {
		sf.toEnd = false
		if v := a.sheetValue(name); v != nil {
			n := utf8.RuneCountInString(*v)
			in.SetTextSelection(n, n)
		}
	}
	enter := sf.enter
	sf.enter = false
	return enter
}

// sheetBuilt reports whether the sheet name bound its field in the last
// frame built.
func (a *App) sheetBuilt(name string) bool {
	if name == "confirm" {
		return a.sf.cpass >= a.ko.pass-1
	}
	return a.sf.name == name && a.sf.pass >= a.ko.pass-1
}

// sheetValue is the string a sheet's field edits, nil for none.
func (a *App) sheetValue(name string) *string {
	switch name {
	case "prompt":
		if a.prompt != nil {
			return &a.prompt.value
		}
	case "branch":
		if a.wtAsk != nil {
			return &a.wtAsk.branch
		}
	case "palette":
		return &a.pal.query
	case "help":
		return &a.help.query
	}
	return nil
}

// replaySheet writes kept keys into the field of the sheet name: text at
// its end, Backspace, Enter where the sheet submits, the palette's Up and
// Down. Any other key, and every key of a sheet with no such field, drops.
func (a *App) replaySheet(name string, q []heldKey) {
	if name == "confirm" {
		a.replayConfirm(q)
		return
	}
	v := a.sheetValue(name)
	if v == nil || !a.sheetBuilt(name) {
		for _, k := range q {
			keyAudit("drop", k.name(), "sheet "+name)
		}
		return
	}
	wrote := false
	for _, k := range q {
		switch {
		case k.text != "":
			if name == "prompt" && !a.prompt.selected {
				// The field selects the old name as it takes the focus,
				// so that typing replaces it: kept text replaces it too.
				a.prompt.selected = true
				*v = ""
			}
			*v += k.text
			wrote = true
		case k.key == ui.KeyBackspace && k.mods == 0:
			if _, n := utf8.DecodeLastRuneInString(*v); n > 0 {
				*v = (*v)[:len(*v)-n]
				wrote = true
			}
		case k.key == ui.KeyEnter && k.mods == 0 && name != "help":
			a.sf.enter = true
		case name == "palette" && k.mods == 0 && (k.key == ui.KeyDown || k.key == ui.KeyUp):
			d := 1
			if k.key == ui.KeyUp {
				d = -1
			}
			a.paletteStep(d, len(a.paletteRanked()))
		default:
			keyAudit("drop", k.name(), "sheet "+name)
		}
	}
	if wrote {
		a.sf.toEnd = true
		if name == "palette" {
			a.pal.index, a.pal.reveal = 0, true
		}
	}
}

// replayConfirm gives kept keys to the confirm dialog: Enter answers with
// its action and Escape with Cancel. Every other key drops, with a line in
// the key audit: a typed key never confirms a destructive dialog, and
// never goes on to the pane it would close (Mac round 6, M1). The keys
// after the answer wait in the queue, and land where the keys go once the
// dialog closed.
func (a *App) replayConfirm(q []heldKey) {
	keep := func(rest []heldKey) {
		if len(rest) == 0 {
			return
		}
		a.ko.mu.Lock()
		a.ko.queue = append(slices.Clone(rest), a.ko.queue...)
		a.ko.mu.Unlock()
	}
	if a.sf.answer != 0 {
		keep(q) // the dialog did not read its answer yet
		return
	}
	for i, k := range q {
		if k.text == "" && k.mods == 0 && (k.key == ui.KeyEnter || k.key == ui.KeyEscape) {
			a.sf.answer = k.key
			keep(q[i+1:])
			return
		}
		keyAudit("drop", k.name(), "sheet confirm")
	}
}

// confirmKeys binds the confirm dialog of this window for the owner, as
// its build makes it, and reports the kept answer: Enter or Escape, or 0.
func (a *App) confirmKeys(cancel ui.Element) ui.Key {
	sf := &a.sf
	cancel.Bind(&sf.cancel)
	sf.cpass = a.ko.pass
	k := sf.answer
	sf.answer = 0
	return k
}
