package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The sheet's look: a dialog over the agents view, two columns as the TUI's
// (the task and the agent's choices at the left, the machines at the
// right), the attempts under both, and the hints and the buttons at the
// foot. Every label is one line; the notice says why Start did nothing.

const (
	spawnW      = 760 // the sheet's widest
	spawnSideW  = 288 // the machines column
	spawnStackW = 620 // under this, the machines go under the choices
	spawnLabelW = 96  // the label column of a field row
)

// spawnHandles are the sheet's fields that a notice or a key puts the focus
// in.
type spawnHandles struct {
	task, agent, model, branch, base, machine, attempts ui.Handle
}

func (h *spawnHandles) of(f spawnField) *ui.Handle {
	switch f {
	case spAgent:
		return &h.agent
	case spModel:
		return &h.model
	case spBranch:
		return &h.branch
	case spBase:
		return &h.base
	case spMachine:
		return &h.machine
	case spAttempts:
		return &h.attempts
	}
	return &h.task
}

// spawnView draws the sheet while it is open.
func (a *App) spawnView(c *ui.Context, pal *palette) {
	s := a.ck.spawn
	if s == nil || !a.ck.open {
		return
	}
	h := &a.ck.spawnUI
	start := s.wantStart
	s.wantStart = false
	ui.DialogBase(c.Key("spawn"), &s.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		ww, wh := c.Size()
		w := min(float32(spawnW), ww-2*sp4)
		floating(panel.Width(w).MaxHeight(wh-2*sp4).Padding(sp5).Gap(sp4).Radius(rSheet).Background(pal.popover), pal).Label(locales.SpawnTitle())
		switch {
		case c.Shortcut(ui.Cmd, ui.KeyEnter):
			start = true
		case c.Shortcut(0, ui.KeyEnter):
			start = true
		case c.Shortcut(ui.Cmd, ui.KeyS):
			a.spawnSavePreset(s, true)
		}
		a.spawnHeader(c, pal, s)
		ui.Scroll(c.Key("body")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(sp4).Padding(2).Children(func() {
				if a.spawnTask(c, pal, s, h) {
					start = true
				}
				stack := w < spawnStackW
				cols := ui.Row(c).Gap(sp6).AlignItems(ui.Start)
				if stack {
					cols = ui.Column(c).Gap(sp4)
				}
				cols.Children(func() {
					ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(sp3).Children(func() {
						if a.spawnChoices(c, pal, s, h) {
							start = true
						}
					})
					col := ui.Column(c).Gap(sp2)
					if stack {
						col.Grow(1)
					} else {
						col.Width(spawnSideW).Shrink(0)
					}
					col.Children(func() { a.spawnMachines(c, pal, s, h) })
				})
				ui.Box(c).Height(1).Background(pal.divider)
				a.spawnAttempts(c, pal, s, h)
			})
		})
		a.spawnFoot(c, pal, s, h, &start)
	})
	if s.focusAgain {
		// At once, in the frame the sheet opens: its fields are bound
		// now, and a key typed before the next frame lands in the field.
		// The route that opened the sheet may take the focus back after
		// this frame: the palette gives it back to the field it came
		// from as it closes, the agents view to its list as it opens. So
		// the sheet asks again each frame until the field has it, for a
		// few frames (QA-064).
		f := h.of(s.focusField)
		s.focusTries++
		switch {
		case s.focusTries > 10 || f.FocusWithin(c) && s.focusTries > 3:
			s.focusAgain, s.focusTries = false, 0
		default:
			if !f.FocusWithin(c) {
				f.Focus()
			}
			c.Invalidate()
		}
	}
	if start && s.open {
		a.spawnStart()
		return
	}
	if !s.open && a.ck.spawn == s {
		a.spawnClose()
	}
}

// spawnHeader is the title, what the agent starts from, and close.
func (a *App) spawnHeader(c *ui.Context, pal *palette, s *spawnSheet) {
	ui.Row(c).Gap(sp3).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, locales.SpawnTitle()).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
		// The real target: the folder, then the session it goes in, then
		// the branch it starts from (council round 4, workflow S1).
		var parts []string
		if s.folder != "" {
			parts = append(parts, locales.SpawnIn(a.local().shortDir(s.folder)))
		}
		if l := a.spawnSessionLabel(s.session); l != "" {
			parts = append(parts, l)
		}
		if s.repo != "" && s.head != "" {
			parts = append(parts, locales.SpawnFromBranch(s.repo, s.head))
		}
		from := strings.Join(parts, " · ")
		if s.card != nil {
			from += " · " + locales.SpawnForCard(s.card.id)
		}
		oneLine(c, from, fsSmall, 0).TextColor(pal.textFaint)
		ui.Spacer(c)
		iconButton(c, "spawn-close", closeIcon, locales.SpawnClose()+" (Esc)", pal, func() { s.open = false })
	})
}

// spawnLabel is a field's label: its name, the accent while it holds the
// focus, the attention colour while the notice points at it.
func spawnLabel(c *ui.Context, pal *palette, s *spawnSheet, f spawnField, text string, focused bool) {
	col := pal.textMuted
	switch {
	case s.notice != "" && s.noticeAt == f:
		col = pal.attention
	case focused:
		col = pal.text
	}
	ui.Text(c, text).FontSize(fsSmall).FontWeight(wMedium).TextColor(col).SingleLine()
}

// spawnTask is the task box: the task in prose, the images pasted or
// dropped, and its keys. It reports whether Enter asked to start.
func (a *App) spawnTask(c *ui.Context, pal *palette, s *spawnSheet, h *spawnHandles) bool {
	start := false
	ui.Column(c).Gap(6).Children(func() {
		spawnLabel(c, pal, s, spTask, locales.SpawnTask(), h.task.FocusWithin(c))
		box := ui.Column(c).Radius(rControl).Padding(8, 10, 6, 10).Gap(6).AlignItems(ui.Stretch).Cursor(ui.CursorText)
		fieldFace(box, h.task.FocusWithin(c), pal)
		if files := box.DroppedFiles(); len(files) > 0 {
			a.addImageFiles(&s.task, files)
		}
		if box.FileDragOver() {
			box.Background(pal.accent.Alpha(0.08))
		}
		box.Children(func() {
			if len(s.task.images) > 0 {
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for i := range s.task.images {
						a.imageChip(c, pal, &s.task, i)
					}
				})
			}
			in := ui.TextAreaBase(c.Key("task"), &s.task.text).Bind(&h.task).MinHeight(66).MaxHeight(176).
				FontSize(fsProse).LineHeight(1.5).TextColor(pal.text).Placeholder(locales.SpawnTaskPlaceholder()).Label(locales.SpawnTask())
			in.OnPaste(func(text string) bool {
				if text != "" {
					return false
				}
				return a.pasteImageInto(&s.task)
			})
			if in.Changed() {
				s.followTask()
				if s.noticeAt == spTask {
					s.notice = ""
				}
			}
			in.OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEscape && k.Mods == 0 {
					// One Escape closes the sheet: the box would take the
					// first for itself, and the sheet stayed open.
					s.task.text = k.Text()
					a.update(a.spawnClose)
					return true
				}
				if k.Key == ui.KeyEnter && (k.Mods == 0 || k.Mods == ui.Cmd) {
					// This runs between frames: the next frame starts.
					s.task.text = k.Text()
					s.followTask()
					s.wantStart = true
					a.update(func() {})
					return true
				}
				return false
			})
			ui.Row(c).Gap(sp3).AlignItems(ui.Center).Clip().Children(func() {
				keyHint(c, pal, keyName("CmdOrCtrl+V"), locales.CockpitHintImage())
				keyHint(c, pal, "⇧↵", locales.CockpitHintNewLine())
			})
		})

	})
	return start
}

// spawnChoices are the agent, the model, where it works, and for a new
// worktree its branch and base. It reports whether Enter in a field asked
// to start.
func (a *App) spawnChoices(c *ui.Context, pal *palette, s *spawnSheet, h *spawnHandles) bool {
	start := false
	// Agent: the first agents as chips, the rest in a list.
	ui.Column(c).Gap(6).Children(func() {
		spawnLabel(c, pal, s, spAgent, locales.SpawnAgent(), h.agent.FocusWithin(c))
		a.spawnAgents(c, pal, s, h)
	})
	// Model: a field, with the models running agents report.
	ui.Column(c).Gap(6).Children(func() {
		spawnLabel(c, pal, s, spModel, locales.SpawnModel(), h.model.FocusWithin(c))
		var models []string
		if s.agent < len(s.agents) {
			models = s.models[s.agents[s.agent].id]
		}
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			if a.spawnCombo(c, pal, "model", &s.model, locales.SpawnModelDefault(), models, &h.model, 240) {
				start = true
			}
			if strings.TrimSpace(s.model) != "" {
				oneLine(c, locales.SpawnModelPassed(), fsSmall, 0).TextColor(pal.textFaint)
			}
		})
	})
	// Where it works.
	ui.Column(c).Gap(6).Children(func() {
		spawnLabel(c, pal, s, spWhere, locales.SpawnWhere(), false)
		ui.Row(c).Children(func() {
			segmented(c, "where", &s.where, []string{"worktree", "folder"}, []string{locales.SpawnNewWorktree(), locales.SpawnThisFolder()})
		})
		if s.whereSeen != "" && s.where != s.whereSeen {
			a.spawnSetWhere(s, s.where)
		}
		s.whereSeen = s.where
	})
	if !s.worktree() {
		return start
	}
	ui.Column(c).Gap(6).Children(func() {
		spawnLabel(c, pal, s, spBranch, locales.SpawnBranch(), h.branch.FocusWithin(c))
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			frame := ui.Row(c.Key("branch")).Cursor(ui.CursorText).Grow(1).MaxWidth(320)
			var in ui.Element
			frame.Children(func() {
				in = ui.TextInputBase(c.Key("in"), &s.branch).Bind(&h.branch).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).Padding(0, 8).
					FontSize(fsBody).Placeholder("agent/name").Label(locales.SpawnBranch())
			})
			field(c, frame, in.Focused(), pal).Padding(0)
			if in.Submitted() {
				start = true
			}
			if in.Changed() && s.noticeAt == spBranch {
				s.notice = ""
			}
			if b := strings.TrimSpace(s.branch); b != "" && b == s.branchAuto {
				oneLine(c, locales.SpawnFromTask(), fsSmall, 0).TextColor(pal.textFaint)
			}
		})
	})
	ui.Column(c).Gap(6).Children(func() {
		spawnLabel(c, pal, s, spBase, locales.SpawnBase(), h.base.FocusWithin(c))
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			if a.spawnCombo(c, pal, "base", &s.base, "HEAD", s.branches, &h.base, 200) {
				start = true
			}
			if s.dirty > 0 && s.host() == "" {
				oneLine(c, locales.SpawnDirty(s.dirty), fsSmall, 0).TextColor(pal.textFaint)
			}
		})
	})
	return start
}

// spawnCombo is a text field with a list of choices under it, which Down or
// typing opens. It reports whether Enter asked to start.
func (a *App) spawnCombo(c *ui.Context, pal *palette, key string, value *string, placeholder string, options []string, bind *ui.Handle, width float32) bool {
	start := false
	cb := ui.ComboboxBase(c.Key(key), value)
	cb.Input.Bind(bind).Width(width).Height(controlH+2).Padding(0, 8).FontSize(fsBody).Placeholder(placeholder).Label(placeholder)
	field(c, cb.Input, cb.Input.Focused(), pal)
	if cb.Input.Submitted() {
		start = true
	}
	if len(options) > 0 {
		cb.Popup(func(panel ui.Element) {
			menuPanel(panel, pal).Margin(4, 0, 0, 0).Width(max(width, 220)).MaxHeight(240)
			q := strings.ToLower(strings.TrimSpace(*value))
			shown := 0
			for _, o := range options {
				if q != "" && !strings.Contains(strings.ToLower(o), q) && !slices.Contains(options, *value) {
					continue
				}
				shown++
				item := cb.Item(o)
				menuRow(c, item, o, o == *value, item.Highlighted(), pal)
			}
			if shown == 0 {
				ui.Text(c, locales.CommonNoResults()).FontSize(fsBody).TextColor(pal.textFaint).Padding(6, 12)
			}
		})
		if v, ok := cb.Chosen(); ok {
			*value = v
		}
	}
	return start
}

// spawnAgents is the agent picker: the first four agents as chips, the rest
// in a list. Left and Right step the agent, Down or Space opens the list.
func (a *App) spawnAgents(c *ui.Context, pal *palette, s *spawnSheet, h *spawnHandles) {
	// The list closed by Esc or a pick gives the focus back to the field.
	if s.listWas && !s.listOpen {
		a.defer_(func() { h.agent.Focus() })
	}
	s.listWas = s.listOpen
	row := ui.Row(c.Key("agents")).Gap(6).AlignItems(ui.Center).Wrap().Focusable().Bind(&h.agent).Label(locales.SpawnAgent()).Radius(rControl)
	n := len(s.agents)
	switch {
	case n == 0:
	case row.Shortcut(0, ui.KeyLeft), row.Shortcut(0, ui.KeyH):
		s.agent = (s.agent - 1 + n) % n
		a.spawnAgentPicked(s)
	case row.Shortcut(0, ui.KeyRight), row.Shortcut(0, ui.KeyL):
		s.agent = (s.agent + 1) % n
		a.spawnAgentPicked(s)
	case row.Shortcut(0, ui.KeyDown), row.Shortcut(0, ui.KeySpace):
		s.listOpen = true
	}
	var more ui.Element
	hasMore := false
	row.Children(func() {
		if !s.read && n == 0 {
			ui.Spinner(c)
			return
		}
		if n == 0 {
			ui.Text(c, locales.SpawnNoAgentFound()).FontSize(fsSmall).TextColor(pal.attention).SingleLine()
			return
		}
		// The chips: the first four, and the picked one when it is
		// further down the list.
		shown := min(n, 4)
		if s.agent >= shown {
			shown--
		}
		for i := range n {
			if i >= shown && i != s.agent {
				continue
			}
			x := s.agents[i]
			b := ui.Row(c.Key("ag-"+x.id)).Height(hControlSm).Padding(0, 10).Gap(6).Radius(rRow).AlignItems(ui.Center).Shrink(0).
				Label(x.cmd).Cursor(ui.CursorPointer)
			fg, bg := pal.text, pal.chip
			switch {
			case i == s.agent:
				fg, bg = onAccent(pal), pal.accent
			case !x.installed && x.seen == 0:
				fg = pal.textFaint
			case b.Hovered():
				bg = pal.rowHover
			}
			b.Background(bg).TextColor(fg)
			if b.Clicked() {
				c.Invalidate()
				s.agent = i
				a.spawnAgentPicked(s)
				h.agent.Focus()
			}
			b.Tooltip(spawnAgentNote(x))
			b.Children(func() {
				ui.Icon(c, harnessIcon(x.id)).Size(12, 12).Shrink(0)
				ui.Text(c, x.cmd).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
			})
		}
		hasMore = true
		more = ui.Row(c.Key("ag-more")).Height(hControlSm).Padding(0, 8).Gap(4).Radius(rRow).AlignItems(ui.Center).Shrink(0).
			Label(locales.SpawnMoreAgents()).Cursor(ui.CursorPointer)
		if more.Hovered() {
			more.Background(pal.rowHover)
		}
		if more.Clicked() {
			c.Invalidate()
			s.listOpen = !s.listOpen
		}
		more.Children(func() {
			ui.Text(c, locales.SpawnMoreAgents()).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
			ui.Icon(c, chevronDownIcon).Size(12, 12).TextColor(pal.textFaint)
		})
	})
	if !hasMore {
		return
	}
	ui.PopoverBase(c.Key("ag-list"), more, &s.listOpen, func(panel ui.Element) {
		menuPanel(panel, pal).Margin(4, 0, 0, 0).Width(280).MaxHeight(300)
		// The arrows move among the agents; the picked one has the focus
		// as the list opens.
		ui.Scroll(c.Key("ag-items")).MaxHeight(290).FocusGroup(ui.Vertical).Children(func() {
			for i, x := range s.agents {
				item := ui.ButtonBase(c.Key("li-"+x.id)).Height(menuItemH).Padding(0, 8).Gap(8).Radius(rRow).AlignItems(ui.Center).Label(x.cmd).FocusRing(false)
				if i == s.agent {
					item.AutoFocus()
				}
				hi := item.Hovered() || item.Focused()
				fg := pal.text
				if hi {
					item.Background(pal.accent)
					fg = onAccent(pal)
				}
				if item.Clicked() {
					s.agent, s.listOpen = i, false
					a.spawnAgentPicked(s)
				}
				item.TextColor(fg).Children(func() {
					mark := ui.Icon(c, checkIcon).Size(12, 12).Shrink(0)
					if i != s.agent {
						mark.Opacity(0)
					}
					ui.Icon(c, harnessIcon(x.id)).Size(12, 12).Shrink(0)
					oneLine(c, x.cmd, fsBody, 0)
					ui.Spacer(c)
					note := oneLine(c, spawnAgentNote(x), fsSmall, 0)
					if !hi {
						note.TextColor(pal.textFaint)
					}
				})
			}
		})
	})
}

// spawnAgentNote says how an agent stands on this machine.
func spawnAgentNote(x spawnAgent) string {
	switch {
	case x.seen > 0:
		return locales.SpawnAgentRunning(x.seen)
	case x.installed:
		return locales.SpawnAgentInstalled()
	}
	return locales.SpawnAgentMissing()
}

// spawnAgentPicked follows a new agent: attempt A is always it, and a
// notice about the agent is gone.
func (a *App) spawnAgentPicked(s *spawnSheet) {
	if len(s.vary) > 0 {
		s.vary[0] = s.agent
	}
	if s.noticeAt == spAgent {
		s.notice = ""
	}
}

// spawnMachines is the machine list, best fit first.
func (a *App) spawnMachines(c *ui.Context, pal *palette, s *spawnSheet, h *spawnHandles) {
	focused := h.machine.FocusWithin(c)
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		spawnLabel(c, pal, s, spMachine, locales.SpawnMachine(), focused)
		ui.Spacer(c)
		ui.Text(c, locales.SpawnBestFit()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
	})
	col := ui.Column(c.Key("machines")).Gap(4).Focusable().Bind(&h.machine).Label(locales.SpawnMachine()).Radius(rControl).AlignItems(ui.Stretch)
	switch {
	case col.Shortcut(0, ui.KeyUp), col.Shortcut(0, ui.KeyK):
		a.spawnPickMachine(s, s.machine-1)
	case col.Shortcut(0, ui.KeyDown), col.Shortcut(0, ui.KeyJ):
		a.spawnPickMachine(s, s.machine+1)
	}
	agent := ""
	if s.agent < len(s.agents) {
		agent = s.agents[s.agent].cmd
	}
	col.Children(func() {
		for i, m := range s.machines {
			picked := i == s.machine
			row := ui.Column(c.Key("m-"+m.host)).Padding(7, 10).Gap(3).Radius(rControl).Label(m.name).Cursor(ui.CursorPointer)
			if !m.up {
				row.Opacity(0.55)
			}
			switch {
			case picked:
				chosen(row.Background(pal.rowSelected), pal)
				row.InsetShadow(0, 0, 0, 1, pal.accent.Alpha(0.55))
			case row.Hovered() && m.up:
				row.Background(pal.rowHover)
			}
			if row.Clicked() {
				c.Invalidate()
				a.spawnPickMachine(s, i)
				h.machine.Focus()
			}
			row.Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					spawnRadio(c, pal, picked)
					// The name keeps its room: "this machine" gives way first.
					oneLine(c, m.name, fsBody, wMedium).TextColor(pal.text).Shrink(0).MaxWidth(150)
					if m.host == "" {
						oneLine(c, locales.SpawnThisMachine(), fsSmall, 0).TextColor(pal.textFaint).Shrink(2)
					}
					ui.Spacer(c)
					if m.loadOK && m.cores > 0 {
						frac := min(max(m.load/float64(m.cores), 0), 1)
						spawnBar(c, pal, float32(frac))
						ui.Text(c, fmt.Sprintf("%d%%", int(frac*100+0.5))).FontSize(fsSmall).TextColor(pal.textMuted).FontFeatures("tnum").SingleLine().Shrink(0)
					}
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 20).Children(func() {
					var sub string
					switch {
					case !m.up:
						sub = locales.SpawnNotConnected()
					case m.cores > 0:
						sub = locales.SpawnCoresWorking(m.cores, m.working)
					default:
						sub = locales.SpawnWorking(m.working)
					}
					oneLine(c, sub, fsSmall, 0).TextColor(pal.textFaint)
					ui.Spacer(c)
					if picked && agent != "" {
						ready := locales.SpawnAgentReady(agent)
						if m.host == "" && s.agent < len(s.agents) && !s.agents[s.agent].installed {
							ready = locales.SpawnAgentMissing()
						}
						oneLine(c, ready, fsSmall, 0).TextColor(pal.textMuted).Shrink(2)
					}
				})
			})
		}
	})
	ui.Text(c, locales.SpawnOrderHolds()).FontSize(fsSmall).TextColor(pal.textFaint).MaxLines(2)
}

// spawnRadio is a radio mark: a ring, filled with the accent when on.
func spawnRadio(c *ui.Context, pal *palette, on bool) {
	b := ui.Box(c).Size(12, 12).Radius(6).Shrink(0).Center()
	if on {
		b.Background(pal.accent).Children(func() {
			ui.Box(c).Size(4, 4).Radius(2).Background(onAccent(pal))
		})
		return
	}
	b.InsetShadow(0, 0, 0, 1.2, pal.textFaint)
}

// spawnBar is a load bar 56 wide.
func spawnBar(c *ui.Context, pal *palette, frac float32) {
	col := pal.work
	if frac > 0.8 {
		col = pal.attention
	}
	ui.Box(c).Width(56).Height(4).Radius(2).Background(pal.chip).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: max(r.W*frac, 2), H: r.H}, col, 2)
	})
}

// spawnAttempts is the attempts stepper, what it means, and an agent chip
// per attempt.
func (a *App) spawnAttempts(c *ui.Context, pal *palette, s *spawnSheet, h *spawnHandles) {
	row := ui.Row(c.Key("attempts")).Gap(sp3).AlignItems(ui.Center).Focusable().Bind(&h.attempts).Label(locales.SpawnAttempts()).Radius(rControl)
	switch {
	case row.Shortcut(0, ui.KeyLeft), row.Shortcut(0, ui.KeyMinus), row.Shortcut(0, ui.KeyH):
		a.spawnSetAttempts(s, s.attempts-1)
	case row.Shortcut(0, ui.KeyRight), row.Shortcut(0, ui.KeyEqual), row.Shortcut(ui.Shift, ui.KeyEqual), row.Shortcut(0, ui.KeyL):
		a.spawnSetAttempts(s, s.attempts+1)
	case row.Shortcut(0, ui.KeyV):
		a.spawnVary(s)
	}
	for k := ui.Key1; k <= ui.Key8; k++ {
		if row.Shortcut(0, k) {
			a.spawnSetAttempts(s, int(k-ui.Key1)+1)
		}
	}
	row.Children(func() {
		ui.Box(c).Width(spawnLabelW - sp3).Shrink(0).Children(func() {
			spawnLabel(c, pal, s, spAttempts, locales.SpawnAttempts(), h.attempts.FocusWithin(c))
		})
		step := func(key, label string, d int) {
			b := ui.Row(c.Key(key)).Size(hControlSm, hControlSm).Radius(rRow).Center().Label(label).Cursor(ui.CursorPointer)
			if b.Hovered() {
				b.Background(pal.rowHover)
			}
			if b.Clicked() {
				c.Invalidate()
				a.spawnSetAttempts(s, s.attempts+d)
				h.attempts.Focus()
			}
			b.Children(func() { ui.Text(c, map[int]string{-1: "−", 1: "+"}[d]).FontSize(fsBody).TextColor(pal.textMuted) })
		}
		ui.Row(c).Height(hControlSm).Radius(rControl).Background(pal.chip).AlignItems(ui.Center).Shrink(0).Children(func() {
			step("fewer", locales.SpawnFewer(), -1)
			ui.Text(c, itoa(s.attempts)).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum").Width(20).TextAlign(ui.Center)
			step("more", locales.SpawnMore(), 1)
		})
		words := locales.SpawnOneAttempt()
		if s.attempts > 1 {
			words = locales.SpawnManyAttempts(s.attempts)
		}
		oneLine(c, words, fsSmall, 0).TextColor(pal.textMuted)
	})
	if s.attempts < 2 {
		return
	}
	host := ""
	if s.machine < len(s.machines) {
		host = s.machines[s.machine].name
	}
	ui.Row(c).Gap(sp2).Wrap().AlignItems(ui.Center).Padding(0, 0, 0, spawnLabelW).Children(func() {
		for i := range s.attempts {
			ai := s.attemptAgent(i)
			if ai >= len(s.agents) {
				continue
			}
			name := s.agents[ai].cmd
			if m := strings.TrimSpace(s.model); m != "" && ai == s.agent {
				name += " " + m
			}
			b := ui.Row(c.Key(fmt.Sprint("chip", i))).Height(hControlSm).Padding(0, 8, 0, 4).Gap(6).Radius(rRow).AlignItems(ui.Center).
				Background(pal.chip).Label(locales.SpawnAttemptChip(ckLetter(i), name))
			if i > 0 {
				b.Tooltip(locales.SpawnAttemptNext())
				if b.Hovered() {
					b.Background(pal.rowHover)
				}
				if b.Clicked() && len(s.agents) > 0 {
					c.Invalidate()
					s.vary[i] = (s.vary[i] + 1) % len(s.agents)
				}
			}
			b.Children(func() {
				ui.Box(c).Size(16, 16).Radius(rChip).Background(pal.accent.Alpha(0.18)).Center().Shrink(0).Children(func() {
					ui.Text(c, ckLetter(i)).FontSize(fsCaption).FontWeight(wSemibold).TextColor(pal.accent)
				})
				oneLine(c, name, fsSmall, wMedium).TextColor(pal.text).MaxWidth(160)
				oneLine(c, host, fsSmall, 0).TextColor(pal.textFaint).MaxWidth(90)
			})
		}
		v := ui.Row(c.Key("vary")).Height(hControlSm).Padding(0, 8).Radius(rRow).AlignItems(ui.Center).Label(locales.SpawnVary()).Cursor(ui.CursorPointer)
		if v.Hovered() {
			v.Background(pal.rowHover)
		}
		if v.Clicked() {
			c.Invalidate()
			a.spawnVary(s)
		}
		v.Children(func() {
			ui.Text(c, "+ "+locales.SpawnVary()).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
		})
	})
}

// spawnFoot is the notice, the hints and the buttons.
func (a *App) spawnFoot(c *ui.Context, pal *palette, s *spawnSheet, h *spawnHandles, start *bool) {
	switch {
	case s.notice != "":
		ui.Text(c, s.notice).FontSize(fsSmall).TextColor(pal.attention).MaxLines(2).Shrink(0)
	case !s.read:
		ui.Text(c, locales.SpawnReadingAll()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Shrink(0)
	}
	ui.Row(c).Gap(sp3).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Row(c).Gap(sp3).AlignItems(ui.Center).Grow(1).MinWidth(0).Clip().Children(func() {
			keyHint(c, pal, "↵", locales.SpawnHintStart())
			keyHint(c, pal, "Tab", locales.SpawnHintNext())
			keyHint(c, pal, keyName("CmdOrCtrl+S"), locales.SpawnHintPreset())
		})
		if button(c, "spawn-cancel", locales.CommonCancel(), "Esc", false, pal) {
			s.open = false
		}
		label := locales.SpawnStartOne()
		if s.attempts > 1 {
			label = locales.SpawnStartMany(s.attempts)
		}
		if button(c, "spawn-start", label, "", true, pal) {
			*start = true
		}
	})
}
