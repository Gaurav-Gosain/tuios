package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The task board: a session's task cards as kanban columns, one per stage
// in stage order, as tuios PR #1548 draws them in the TUI. A column shows
// its count; Done starts folded and lists its newest card first. A card
// shows its title, its holder's state and name, or Blocked and why, or
// free, and its id and age.
//
// Keys while the board holds the focus: h l and the arrows go between
// columns, j k between cards, H L (Shift with the arrows) move the card a
// stage, Enter goes to the holder's pane, n adds a card to the column, t
// takes the card for the selected agent. A click selects a card, a double
// click goes to the pane, and a drag onto another column moves the card.

// ckStages are the stages, in order.
var ckStages = []string{"todo", "plan", "doing", "verify", "review", "shipped", "done"}

func ckStageTitle(s string) string {
	switch s {
	case "todo":
		return locales.CockpitStageTodo()
	case "plan":
		return locales.CockpitStagePlan()
	case "doing":
		return locales.CockpitStageDoing()
	case "verify":
		return locales.CockpitStageVerify()
	case "review":
		return locales.CockpitStageReview()
	case "shipped":
		return locales.CockpitStageShipped()
	}
	return locales.CockpitStageDone()
}

// ckBoard is the board's state.
type ckBoard struct {
	loading bool
	err     string
	session string
	cards   []session.TaskRow
	sel     string // the selected card's id
	col     int    // the column the cursor is in, for an empty one
	opened  map[string]bool
	focus   ui.Handle
	adding  int // the column a new card is typed into, -1 for none
	draft   string
}

func (a *App) fetchBoard() {
	cl := a.cockpitClient()
	t := &a.ck.tabs
	if t.board == nil {
		t.board = &ckBoard{opened: map[string]bool{}, adding: -1}
	}
	b := t.board
	if cl == nil || b.loading {
		return
	}
	b.loading = true
	sess := b.session
	go func() {
		var out struct {
			Tasks []session.TaskRow `json:"tasks"`
		}
		p := map[string]any{"all_sessions": true}
		if sess != "" {
			p = map[string]any{"session": sess}
		}
		err := cl.VerbInto("list-tasks", p, false, &out)
		a.update(func() {
			b.loading = false
			if err != nil {
				b.err = err.Error()
				return
			}
			b.err, b.cards = "", out.Tasks
		})
	}()
}

// column is the cards of a stage, Done's newest first.
func (b *ckBoard) column(stage string) []session.TaskRow {
	var out []session.TaskRow
	for _, t := range b.cards {
		if t.Stage == stage {
			out = append(out, t)
		}
	}
	if stage == "done" {
		slices.Reverse(out)
	}
	return out
}

func (b *ckBoard) folded(stage string) bool {
	return stage == "done" && !b.opened[stage]
}

// cursor is the selected card's column and row, -1 when none is.
func (b *ckBoard) cursor() (int, int) {
	for ci, st := range ckStages {
		for ri, t := range b.column(st) {
			if t.ID == b.sel && t.Session != "" {
				return ci, ri
			}
		}
	}
	return b.col, -1
}

func (b *ckBoard) card(id string) (session.TaskRow, bool) {
	for _, t := range b.cards {
		if t.ID == id {
			return t, true
		}
	}
	return session.TaskRow{}, false
}

// boardView draws the board of a session, every session's for "".
func (a *App) boardView(c *ui.Context, pal *palette, sess string) {
	t := &a.ck.tabs
	if t.board == nil || t.board.session != sess {
		if t.board != nil {
			t.board.session, t.board.cards, t.board.sel = sess, nil, ""
		} else {
			t.board = &ckBoard{opened: map[string]bool{}, adding: -1, session: sess}
		}
		a.fetchBoard()
	}
	b := t.board
	if b.err != "" {
		a.ckMessage(c, pal, locales.CockpitNoBoard(), b.err)
		return
	}
	root := ui.Column(c.Key("board")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).
		Focusable().FocusRing(false).Bind(&b.focus).Label(locales.CockpitTabBoard())
	if a.boardKeys(root, b) {
		c.Invalidate()
	}
	root.Children(func() {
		// Columns that do not fit scroll sideways, as the TUI's board
		// shows the run round the cursor.
		ui.ScrollHorizontal(c.Key("cols")).Grow(1).MinHeight(0).Children(func() {
			ui.Row(c).FillHeight().AlignItems(ui.Stretch).Gap(10).Padding(14, 16).Children(func() {
				for ci, st := range ckStages {
					a.boardColumn(c, pal, b, ci, st)
				}
			})
		})
	})
}

// boardColumn is one stage's column.
func (a *App) boardColumn(c *ui.Context, pal *palette, b *ckBoard, ci int, st string) {
	cards := b.column(st)
	folded := b.folded(st)
	col := ui.Column(c.Key(st)).Gap(8).Padding(8).Radius(10).Background(ckGround(pal)).AlignItems(ui.Stretch)
	if folded || len(cards) == 0 {
		col.Width(112).Shrink(0)
	} else {
		col.Width(178).Shrink(0)
	}
	if _, over := ui.DragOver[string](col); over {
		col.Border(2, pal.accent)
	}
	if id, ok := ui.Drop[string](col); ok {
		a.moveCard(b, id, st)
	}
	col.Children(func() {
		head := ui.Row(c.Key("h")).Gap(6).AlignItems(ui.Center).Padding(2, 4).Cursor(ui.CursorPointer)
		if head.Clicked() {
			c.Invalidate()
			if st == "done" {
				b.opened[st] = !b.opened[st]
			}
			b.col = ci
		}
		head.Children(func() {
			ui.Text(c, ckStageTitle(st)).FontSize(12).FontWeight(650).TextColor(pal.textMuted).SingleLine()
			ui.Text(c, itoa(len(cards))).FontSize(12).TextColor(pal.textFaint).FontFeatures("tnum")
			ui.Spacer(c)
			add := ui.ButtonBase(c.Key("add")).Size(20, 20).Radius(5).Center().Label(locales.CockpitAddCard()).Tooltip(locales.CockpitAddCard())
			if add.Hovered() {
				add.Background(pal.rowHover)
			}
			add.Children(func() { ui.Icon(c, plusIcon).Size(11, 11).TextColor(pal.textMuted) })
			if add.Clicked() {
				c.Invalidate()
				b.adding, b.draft = ci, ""
			}
		})
		if b.adding == ci {
			in := textField(c, "new-card", &b.draft, locales.CockpitCardPlaceholder(), 0, pal).AutoFocus()
			if in.Submitted() && strings.TrimSpace(b.draft) != "" {
				a.addCard(b, st, b.draft)
				b.adding = -1
			} else if in.Shortcut(0, ui.KeyEscape) {
				b.adding = -1
				b.focus.Focus()
			}
		}
		if folded {
			return
		}
		ui.Scroll(c.Key("cards")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for _, t := range cards {
					a.boardCard(c, pal, b, t)
				}
			})
		})
	})
}

// boardCard is one card.
func (a *App) boardCard(c *ui.Context, pal *palette, b *ckBoard, t session.TaskRow) {
	on := t.ID == b.sel
	card := ui.Column(c.Key(t.Session+"/"+t.ID)).Gap(6).Padding(9, 10).Radius(8).Background(pal.card).Cursor(ui.CursorPointer)
	if on {
		// The selected card drags to another column: a card that drags
		// takes no click, so a click selects it first.
		card.Drag(t.ID)
	}
	switch {
	case on:
		card.Border(1, pal.accent)
	case card.Hovered():
		card.Border(1, pal.divider.Mix(pal.text, 0.2))
	default:
		card.Border(1, pal.divider)
	}
	if card.Dragging() {
		card.Opacity(0.4)
	}
	if (card.Pressed() || card.Clicked()) && b.sel != t.ID {
		b.sel = t.ID
		b.focus.Focus()
		// The click came in this build: build again to show it.
		c.Invalidate()
	}
	if card.DoubleClicked() {
		a.goToHolder(t)
	}
	card.ContextMenu(func(m *ui.Menu) {
		// No state changes here: the builder also runs to collect the
		// menu's shortcuts.
		for _, st := range ckStages {
			if st != t.Stage && m.Item(locales.CockpitMoveTo(ckStageTitle(st))).Chosen() {
				a.moveCard(b, t.ID, st)
			}
		}
		if t.Holder == nil {
			m.Separator()
			if m.Item(locales.CockpitTakeCard()).Chosen() {
				a.takeCard(b, t)
			}
		}
	})
	card.Children(func() {
		ui.Text(c, t.Title).FontSize(13).LineHeight(1.35).TextColor(pal.text).MaxLines(2).Ellipsis("…")
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			switch {
			case t.Blocked != nil:
				ui.Text(c, locales.CockpitBlocked()).FontSize(11.5).FontWeight(650).TextColor(pal.danger).SingleLine()
			case t.Holder != nil:
				st, unread := ckStateStatus(t.Holder.State)
				ckMark(c, pal, st, unread, 11)
				name := t.Holder.Name
				if name == "" {
					name = t.Holder.Window[:min(8, len(t.Holder.Window))]
				}
				ui.Text(c, name).FontSize(11.5).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
			default:
				ui.Text(c, locales.CockpitFree()).FontSize(11.5).TextColor(pal.textFaint)
			}
			ui.Spacer(c)
			ui.Text(c, t.ID).FontSize(11).TextColor(pal.textFaint).FontFeatures("tnum")
			ui.Text(c, ago(t.StageAt/int64(time.Millisecond), c.Now())).FontSize(11).TextColor(pal.textFaint)
		})
	})
}

// ckStateStatus is an agent state as the status mark draws it.
func ckStateStatus(state string) (proto.Status, bool) {
	switch state {
	case "working":
		return proto.Working, false
	case "needs_input":
		return proto.Waiting, true
	case "errored":
		return proto.Failed, true
	case "done":
		return proto.Idle, true
	}
	return proto.Idle, false
}

// boardKeys are the board's keys.
func (a *App) boardKeys(root ui.Element, b *ckBoard) (handled bool) {
	handled = true
	key := func(m ui.Modifiers, k ui.Key) bool { return root.Shortcut(m, k) }
	ci, ri := b.cursor()
	pick := func(ci, ri int) {
		ci = min(max(ci, 0), len(ckStages)-1)
		b.col = ci
		cards := b.column(ckStages[ci])
		if b.folded(ckStages[ci]) || len(cards) == 0 {
			b.sel = ""
			return
		}
		b.sel = cards[min(max(ri, 0), len(cards)-1)].ID
	}
	switch {
	case key(ui.Shift, ui.KeyH), key(ui.Shift, ui.KeyLeft):
		if t, ok := b.card(b.sel); ok && ci > 0 {
			a.moveCard(b, t.ID, ckStages[ci-1])
		}
	case key(ui.Shift, ui.KeyL), key(ui.Shift, ui.KeyRight):
		if t, ok := b.card(b.sel); ok && ci < len(ckStages)-1 {
			a.moveCard(b, t.ID, ckStages[ci+1])
		}
	case key(0, ui.KeyH), key(0, ui.KeyLeft):
		pick(ci-1, max(ri, 0))
	case key(0, ui.KeyL), key(0, ui.KeyRight):
		pick(ci+1, max(ri, 0))
	case key(0, ui.KeyJ), key(0, ui.KeyDown):
		pick(ci, ri+1)
	case key(0, ui.KeyK), key(0, ui.KeyUp):
		pick(ci, ri-1)
	case key(0, ui.KeyEnter):
		if t, ok := b.card(b.sel); ok {
			a.goToHolder(t)
		}
	case key(0, ui.KeyN):
		b.adding, b.draft = ci, ""
	case key(0, ui.KeyT):
		if t, ok := b.card(b.sel); ok {
			a.takeCard(b, t)
		}
	case key(0, ui.KeyBracketLeft):
		a.ckStepTab(-1)
	case key(0, ui.KeyBracketRight):
		a.ckStepTab(1)
	case key(0, ui.KeyEscape):
		a.ck.fleetFocus.Focus()
	default:
		handled = false
	}
	return handled
}

func (a *App) moveCard(b *ckBoard, id, stage string) {
	cl := a.cockpitClient()
	t, ok := b.card(id)
	if cl == nil || !ok || t.Stage == stage {
		return
	}
	// The card moves at once; the daemon's task-changed event confirms it.
	for i := range b.cards {
		if b.cards[i].ID == id && b.cards[i].Session == t.Session {
			b.cards[i].Stage = stage
		}
	}
	b.sel = id
	go func() {
		_, err := cl.Verb("set-task-stage", map[string]any{"session": t.Session, "id": id, "stage": stage}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.fetchBoard()
		})
	}()
}

func (a *App) addCard(b *ckBoard, stage, title string) {
	cl := a.cockpitClient()
	sess := b.session
	if sess == "" {
		if ag, ok := a.ckSelectedAgent(); ok {
			sess = ag.Session
		}
	}
	if cl == nil || sess == "" {
		return
	}
	go func() {
		_, err := cl.Verb("add-task", map[string]any{"session": sess, "title": title, "stage": stage}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.fetchBoard()
		})
	}()
}

// takeCard gives the card to the agent the cockpit has selected.
func (a *App) takeCard(b *ckBoard, t session.TaskRow) {
	cl := a.cockpitClient()
	ag, ok := a.ckSelectedAgent()
	if cl == nil || !ok || ag.Session != t.Session {
		a.toast(locales.CockpitTakeNeedsAgent())
		return
	}
	go func() {
		_, err := cl.Verb("claim-task", map[string]any{"session": t.Session, "id": t.ID, "window": ag.Window}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.fetchBoard()
		})
	}()
}

// goToHolder shows the pane that holds the card.
func (a *App) goToHolder(t session.TaskRow) {
	if t.Holder == nil {
		a.toast(locales.CockpitCardFree())
		return
	}
	for _, ag := range a.ck.agents {
		if ag.Session == t.Session && ag.Window == t.Holder.Window {
			a.goToAgent(ag)
			return
		}
	}
	a.toast(locales.CockpitNoPane())
}
