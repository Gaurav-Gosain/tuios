package main

import (
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// The one owner of the keyboard while the focus moves.
//
// MyGo gives an element the focus in the frame after it is asked
// (Handle.Focus), and an element not built yet takes it only in the frame
// that builds it. So each change of where the keys go has a gap: a
// workspace switch until the new panes attach, the scratch until its
// terminal attaches, a row click until the reply box of the new agent is
// built, the Agents view until its list has the focus. Keys typed in that
// gap went to the element that had the focus before, or nowhere. Each case
// had its own fix: a sink for the switch, a byte route for the scratch, an
// insert string for the reply box, and each lost keys at the edges of the
// others (Mac round 4, W4-M1, F4-M1, F4-M2).
//
// Now a change asks the owner to hold the keys (holdKeys), with a test of
// when it is done. While any hold is on, a sink of no size has the focus
// and keeps every key and text in order; a pane's terminal and the agents
// list that still have the focus give their keys to it too (ownKey). When
// every hold is done the owner lands: keyTarget says where the keys go now,
// the owner asks that element for the focus, and once it has it, sends the
// kept keys to it in order, through the route a key typed there takes.

// How long a hold keeps the keys at most.
const (
	switchWait  = 2 * time.Second // a switch the daemon does not answer
	scratchWait = 5 * time.Second // a scratch the daemon still starts
	replyWait   = time.Second     // a reply box that is not built
	landWait    = time.Second     // a target that does not take the focus
)

// keyOwner holds the keys while the focus moves.
type keyOwner struct {
	holds []keyHold
	// queue is on the main thread, but for the text a terminal writes
	// into a slot (fillSlot), which mu guards.
	mu    sync.Mutex
	queue []heldKey
	ref   ui.Handle // the sink
	// phase is where the owner is (koPhase); koSet alone writes it.
	phase  koPhase
	landAt time.Time // when the phase became koLanding or koOwned
	// acting counts the prefix actions decided and not run yet: until
	// they ran, every key goes to the queue (actStart, actDone). actAt is
	// when the last was decided.
	acting int
	actAt  time.Time
	// sheetAt is when the sheet that has the keyboard took it, zero while
	// none has: kept keys typed before it go to the pane they were typed
	// for, the ones after it to the sheet (landBeforeSheet, replaySheet).
	sheetAt time.Time
	// reply sends the keys to the reply box when they land: text typed
	// in the agents list goes there (ckProse).
	reply bool
	seen  string // the key audit's last focus (auditFocus)
	pass  int    // counts the passes the owner ran
	sink  bool   // the frame builds the sink (keySink)
	// kept are the numbers of the last presses kept (ui.InputEvent.Seq),
	// keptN how many were: a press comes to the queue once (keepKey).
	kept  [32]uint64
	keptN int
	// asked are the panes the owner asked the focus for while it lands: a
	// pane that has the focus a frame later because the owner asked is not
	// a pane the user chose (ownerAsked).
	asked []string
	// has is the target that had the keys in the last frame built: a
	// change whose target has them already lands at once (tryLand), with
	// no frame in between, which a window out of sight does not build.
	has keyTarget
	// boxHas: the reply box had the focus in the last frame built
	// (replyNow).
	boxHas bool
	// listHad: the agents list had the keys in the last frame built
	// (catchListKeys).
	listHad bool
}

// koPhase is the key owner's phase. The owner keeps the keys while it is
// not koIdle, and the queue holds what it kept.
type koPhase uint8

const (
	koIdle    koPhase = iota // keys go where they are typed
	koHolding                // a change is on its way (holdKeys): the sink keeps the keys
	koActing                 // a prefix action is decided, not run (actStart): every key is kept
	koOwned                  // a sheet or a dialog has the keyboard: kept keys go to it or to their pane
	koLanding                // every change is done: the target is asked for the focus, then the keys land
)

// String names a phase for the key audit.
func (p koPhase) String() string {
	return [...]string{"idle", "holding", "acting", "owned", "landing"}[p]
}

// koSet moves the owner to phase next, and notes the move in the key audit.
// It is the one writer of ko.phase: TestKeyOwnerPhaseHasOneWriter holds it.
func (a *App) koSet(next koPhase, why string) {
	ko := &a.ko
	if next == ko.phase {
		return
	}
	keyAudit("phase", ko.phase.String()+">"+next.String(), why)
	if next == koLanding || next == koOwned {
		ko.landAt = time.Now()
		ko.asked = ko.asked[:0]
	}
	ko.phase = next
}

// keyHold is one change on its way.
type keyHold struct {
	why   string
	until time.Time
	ready func() bool
}

// heldKey is one input the owner kept: a key press, or text.
type heldKey struct {
	mods ui.Modifiers
	key  ui.Key
	text string
	// seq is the number of the press (ui.InputEvent.Seq), 0 when it is not
	// known.
	seq uint64
	// slot waits for the text of a key the owner cannot spell: the
	// terminal of pane writes it (fillSlot).
	slot bool
	pane string
	at   time.Time
}

// slotWait bounds how long a slot waits for its text.
const slotWait = 200 * time.Millisecond

// keyTarget is where the keys go once a change is done: a pane's
// terminal, the reply box, the agents list, or the sheet that has the
// keyboard. None of them for nothing.
type keyTarget struct {
	pane        string
	reply, list bool
	sheet       string // sheetName, "other" for a sheet with no field the owner writes
}

// holdKeys has the owner keep the keys until ready reports true, or wait
// passed. A hold of the same why takes the place of the one before.
func (a *App) holdKeys(why string, wait time.Duration, ready func() bool) {
	ko := &a.ko
	ko.holds = slices.DeleteFunc(ko.holds, func(h keyHold) bool { return h.why == why })
	ko.holds = append(ko.holds, keyHold{why: why, until: time.Now().Add(wait), ready: ready})
	if ko.acting == 0 {
		a.koSet(koHolding, why)
	}
	keyAudit("hold", why, auditCaller())
	if a.tryLand() {
		return
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// tryLand lands the keys at once when every hold is done and the target
// had the keys in the last frame, and reports whether it did.
func (a *App) tryLand() bool {
	ko := &a.ko
	if ko.acting > 0 {
		return false
	}
	now := time.Now()
	for _, h := range ko.holds {
		if !h.ready() && now.Before(h.until) && !a.keysOwned() {
			return false
		}
	}
	t := a.keyTarget()
	if t != ko.has || a.slotWaits(now) {
		return false
	}
	ko.holds = nil
	a.koSet(koIdle, "land at once")
	ko.ref.CancelFocus()
	a.replayKeys(t)
	return true
}

// moveKeys has the owner give the keys to keyTarget, with what was typed
// until it has them: a change that is done at once, but whose target takes
// the focus only in a later frame.
func (a *App) moveKeys(why string) {
	a.holdKeys(why, 0, func() bool { return true })
}

// keysHeld reports whether the owner has the keys.
func (a *App) keysHeld() bool {
	ko := &a.ko
	return len(ko.holds) > 0 || ko.phase != koIdle || ko.acting > 0 || len(ko.queue) > 0
}

// ownKey keeps a key that a pane's terminal got while the owner has the
// keys, and reports whether it did. A key that types text keeps its text,
// which the terminal then drops (OnKey took the key). A key the owner
// cannot spell goes on to the terminal, and keeps a slot in the queue for
// the text the terminal writes (fillSlot).
//
// The pane the keys land in keeps its keys when nothing waits: no hold
// that is not done, and nothing kept before them.
func (a *App) ownKey(pane string, ev ui.InputEvent) bool {
	if !a.keysPending(pane) {
		return false
	}
	return a.takeKey(pane, ev)
}

// takeKey keeps a key press for the owner, and reports whether it took
// it: a key that types text with its text, which then does not come.
func (a *App) takeKey(pane string, ev ui.InputEvent) bool {
	mods, key := ev.Mods, ev.Key
	k := heldKey{mods: mods, key: key, seq: ev.Seq}
	switch {
	case key == ui.KeySpace && mods&^ui.Shift == 0:
		k.text = " "
	case typesText(mods, key):
		k.text = keyText(mods, key)
	}
	if _, named := keyString(mods, key); k.text == "" && !named {
		// The owner cannot spell it: the terminal writes its text into
		// the slot (fillSlot), or it comes to the sink as text.
		if pane != "" {
			a.keepKey(heldKey{slot: true, pane: pane, at: time.Now()})
		}
		return false
	}
	a.keepKey(k)
	return true
}

// ownText keeps text that the terminal of pane got while the owner has the
// keys, and reports whether it did: the text of a key whose key down came
// before the owner had them, or text that comes with no key (Linux, an
// input method).
func (a *App) ownText(pane string, ev ui.InputEvent) bool {
	if ev.Text == "" || !a.keysPending(pane) {
		return false
	}
	a.keepKey(heldKey{text: ev.Text, seq: ev.Seq})
	return true
}

// actStart notes a prefix action decided, which runs on a later turn of
// the main thread: until it ran (actDone) every key typed goes to the
// queue, in order, so none reaches the pane the action leaves.
func (a *App) actStart(action string) {
	ko := &a.ko
	ko.acting++
	ko.actAt = time.Now()
	keyAudit("act", action, strconv.Itoa(ko.acting))
	a.koSet(koActing, action)
}

// actDone notes a prefix action that ran, and gives the keys kept since it
// was decided to where the keys go now. A sheet or a dialog the action
// opened takes them; the settings window, which is not this window, takes
// none of them, and they drop.
func (a *App) actDone(action string) {
	ko := &a.ko
	ko.acting = max(ko.acting-1, 0)
	keyAudit("acted", action, strconv.Itoa(ko.acting))
	if settingsActions[action] {
		a.dropKeptSince(ko.actAt, "settings")
	}
	if a.keysOwned() && ko.sheetAt.IsZero() {
		ko.sheetAt = ko.actAt
	}
	a.moveKeys("act")
}

// settingsActions are the prefix actions that open the settings window.
var settingsActions = map[string]bool{"prefix_settings": true, "prefix_keybinds": true, "prefix_agents_settings": true}

// dropKeptSince drops the kept keys typed at or after t, each with a line
// in the key audit.
func (a *App) dropKeptSince(t time.Time, why string) {
	ko := &a.ko
	ko.mu.Lock()
	var gone []heldKey
	ko.queue = slices.DeleteFunc(ko.queue, func(k heldKey) bool {
		if !k.at.Before(t) {
			gone = append(gone, k)
			return true
		}
		return false
	})
	ko.mu.Unlock()
	for _, k := range gone {
		keyAudit("drop", k.name(), why)
	}
}

// keysPending reports whether a key the terminal of pane got must go to the
// owner: a change is on its way, keys wait in the queue, or the keys land
// in another place.
func (a *App) keysPending(pane string) bool {
	ko := &a.ko
	if !a.keysHeld() {
		return false
	}
	if ko.acting > 0 || len(ko.queue) > 0 {
		return true
	}
	for _, h := range ko.holds {
		if !h.ready() && time.Now().Before(h.until) {
			return true
		}
	}
	return a.keyTarget().pane != pane
}

// fillSlot puts text that the terminal of pane wrote into the first slot
// that waits for it, from the terminal's input goroutine, and reports
// whether one did.
func (a *App) fillSlot(pane string, p []byte) bool {
	if _, ok := typedKey(p); !ok {
		return false
	}
	ko := &a.ko
	ko.mu.Lock()
	defer ko.mu.Unlock()
	for i := range ko.queue {
		if k := &ko.queue[i]; k.slot && k.pane == pane {
			k.slot, k.text = false, string(p)
			a.update(func() {})
			return true
		}
	}
	return false
}

// keepKey adds one input to the owner's queue.
func (a *App) keepKey(k heldKey) {
	if k.at.IsZero() {
		k.at = time.Now()
	}
	a.ko.mu.Lock()
	if k.seq != 0 && slices.Contains(a.ko.kept[:], k.seq) {
		// The same press again, as the text of a key down kept with its
		// text: one press types once. Two presses of one key have two
		// numbers, so a double letter stays.
		a.ko.mu.Unlock()
		keyAudit("copy", k.name(), seqNote(k.seq))
		return
	}
	if k.seq != 0 {
		a.ko.kept[a.ko.keptN%len(a.ko.kept)] = k.seq
		a.ko.keptN++
	}
	a.ko.queue = append(a.ko.queue, k)
	a.ko.mu.Unlock()
	keyAudit("keep", k.name(), seqNote(k.seq))
	if !k.slot && a.tryLand() {
		return
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// keyOwnerView runs the owner in each frame: it drops the holds that are
// done, keeps the focus in the sink while one is on, and lands the keys
// once none is.
func (a *App) keyOwnerView(c *ui.Context) {
	ko := &a.ko
	a.focusPrev, a.focusNow = a.focusNow, ""
	if auditCaller() != "" {
		a.auditFocus(c)
	}
	ko.has = keyTarget{}
	ko.boxHas = a.ck.open && a.ck.reply.box.FocusWithin(c)
	switch {
	case a.ck.fleetFocus.FocusWithin(c):
		ko.listHad = true
	case a.ck.open || !a.keysHeld():
		ko.listHad = false
	}

	if t := a.keyTarget(); !ko.ref.Focused(c) {
		if ref := a.targetRef(t); ref != nil && ref.FocusWithin(c) {
			ko.has = t
		}
	}
	ko.sink = false
	ko.pass++
	if !a.keysHeld() {
		return
	}
	if len(ko.holds) == 0 && ko.acting == 0 && ko.phase != koLanding && ko.phase != koOwned && !a.keysOwned() {
		// Only kept keys: they land.
		a.koSet(koLanding, "kept")
	}
	now := time.Now()
	ko.holds = slices.DeleteFunc(ko.holds, func(h keyHold) bool {
		done := h.ready() || now.After(h.until)
		if done {
			keyAudit("done", h.why, "")
		}
		return done
	})
	ko.sink = true
	owned := a.keysOwned()
	if owned {
		// A sheet or a dialog has the keyboard. One that opened on its own
		// takes it now; the keys kept before it go to the pane they were
		// typed for, the ones after it to the sheet (design-G, G2).
		if ko.sheetAt.IsZero() {
			ko.sheetAt = now
		}
		if ko.acting == 0 {
			// Not while an action waits: the keys kept before the sheet
			// go where the keys go once it ran.
			a.koSet(koOwned, a.keyTarget().String())
			a.landBeforeSheet()
		}
	} else {
		ko.sheetAt = time.Time{}
	}
	if len(ko.holds) > 0 && !owned || ko.acting > 0 {
		if ko.acting > 0 {
			a.koSet(koActing, "frame")
		} else {
			a.koSet(koHolding, "frame")
		}
		next := now.Add(landWait)
		for _, h := range ko.holds {
			if h.until.Before(next) {
				next = h.until
			}
		}
		c.After(max(next.Sub(now), 16*time.Millisecond))
		if !ko.ref.Focused(c) && !a.tk.live() {
			ko.ref.Focus()
		}
		return
	}
	if ko.phase != koLanding && !owned {
		a.koSet(koLanding, "done")
		// A request the sink did not take yet must not take the keys
		// from the target later: the sink goes once they land.
		ko.ref.CancelFocus()
	}
	t := a.keyTarget()
	ref := a.targetRef(t)
	if t.reply && ref != nil && !ref.FocusWithin(c) {
		// The draft takes the kept text before the box has the focus: a
		// write to the draft in the frame the box takes typed text drops
		// that text (MyGo keeps the app's string), so none may come after.
		a.replyEarly()
	}
	if t.sheet != "" && t.sheet != "other" {
		// A sheet's field takes the kept text through its string, focused
		// or not: once the sheet built it, the keys land, and the field is
		// asked for the focus for what is typed next.
		if !a.sheetBuilt(t.sheet) && now.Sub(ko.landAt) < landWait {
			c.After(16 * time.Millisecond)
			return
		}
		ko.holds = nil
		a.replayKeys(t)
		if !ref.FocusWithin(c) && now.Sub(ko.landAt) < landWait {
			// The sink keeps what is typed until the field has the
			// focus, and the next frame writes it after this.
			ref.Focus()
			c.After(16 * time.Millisecond)
			return
		}
		ko.asked = ko.asked[:0]
		a.koSet(koIdle, "sheet")
		c.Invalidate()
		return
	}
	if ref != nil && !ref.FocusWithin(c) && now.Sub(ko.landAt) < landWait || a.slotWaits(now) {
		if ref != nil && !ref.FocusWithin(c) {
			ref.Focus()
			if t.pane != "" && !slices.Contains(ko.asked, t.pane) {
				ko.asked = append(ko.asked, t.pane)
			}
		}
		c.After(16 * time.Millisecond)
		return
	}
	ko.asked = ko.asked[:0]
	a.koSet(koIdle, "land")
	if owned {
		// The holds of the switch or the scratch are the pane's, not the
		// sheet's: they go, and the keys after the sheet closes go where
		// the keys go then.
		ko.holds = nil
	}
	a.replayKeys(t)
	c.Invalidate()
}

// landBeforeSheet sends the kept keys typed before the sheet took the
// keyboard to the pane they were typed for, at once: the sheet must not
// take them, and must not hold them until it closes (design-G, G2).
func (a *App) landBeforeSheet() {
	ko := &a.ko
	ko.mu.Lock()
	var early []heldKey
	n := 0
	for _, k := range ko.queue {
		if k.slot || !k.at.Before(ko.sheetAt) {
			break
		}
		n++
	}
	if n > 0 {
		early = slices.Clone(ko.queue[:n])
		ko.queue = slices.Delete(ko.queue, 0, n)
	}
	ko.mu.Unlock()
	if n == 0 {
		return
	}
	t := a.paneTarget()
	keyAudit("land", t.String(), strconv.Itoa(n)+" before the sheet")
	a.replayPane(t.pane, early)
}

// slotWaits reports whether a slot still waits for its text.
func (a *App) slotWaits(now time.Time) bool {
	ko := &a.ko
	ko.mu.Lock()
	defer ko.mu.Unlock()
	for _, k := range ko.queue {
		if k.slot && now.Sub(k.at) < slotWait {
			return true
		}
	}
	return false
}

// keySink builds the sink while the owner has the keys. It comes last in
// the window, after every element without a key of its own: one before
// them would change who they are as it comes and goes, and the terminal
// with the focus would lose it.
func (a *App) keySink(c *ui.Context) {
	ko := &a.ko
	if !ko.sink {
		return
	}
	sink := ui.Column(c.Key("key-sink")).Absolute().Width(0).Height(0).Focusable().FocusRing(false).Bind(&ko.ref)
	sink.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputKeyDown:
			// As a terminal's key (takeKey): a key the owner cannot
			// spell goes on, and its text comes as InputText.
			return a.takeKey("", ev)
		case ui.InputText:
			a.keepKey(heldKey{text: ev.Text, seq: ev.Seq})
			return true
		}
		return false
	})
	sink.TextCaret(ui.Rect{})
	a.catchListKeys(c)
}

// catchListKeys keeps for the owner the keys typed in the agents list
// after the view shut. MyGo routes a key to the element it was typed in,
// or that has the focus; the list reads its keys as shortcuts only while
// the view is built. When a landed Escape closed the view, the list kept
// the focus a frame or two without being built, and what was typed then
// went nowhere: the first letter of a line, or the whole line (Mac round
// 5, feel M1). A key whose element is gone routes to the window: these
// shortcuts read it. Only while the list has or had the keys: a terminal
// on its way out takes its own keys, and reading them here typed them
// twice. A key that typed text keeps the text, and the text that comes
// later to the sink is the same press, which keepKey drops.
func (a *App) catchListKeys(c *ui.Context) {
	if a.ck.open || !a.keysHeld() || a.keysOwned() || !a.ko.listHad {
		return
	}
	for _, k := range proseKeys {
		for _, shift := range []bool{true, false} {
			m := ui.Modifiers(0)
			if shift {
				m = ui.Shift
			}
			if !c.Shortcut(m, k) {
				continue
			}
			h := heldKey{mods: m, key: k, seq: c.ShortcutSeq()}
			if r := keyRune(ckKey{key: k, shift: shift}); r != 0 {
				h.text = string(r)
			}
			a.keepKey(h)
			break
		}
	}
	for _, k := range listKeys {
		if c.Shortcut(k.mods, k.key) {
			a.keepKey(heldKey{mods: k.mods, key: k.key, seq: c.ShortcutSeq()})
		}
	}
}

// keyTarget is where the keys go now: the scratch while it shows, the
// reply box or the list while the agents view shows, else the pane the
// user is in.
func (a *App) keyTarget() keyTarget {
	if a.keysOwned() {
		name := a.sheetName()
		if name == "" {
			name = "other"
		}
		return keyTarget{sheet: name}
	}
	return a.paneTarget()
}

// paneTarget is keyTarget with no sheet open.
func (a *App) paneTarget() keyTarget {
	if q := &a.quick; q.drop {
		if p := scratchPane(a.session(q.session)); p != nil {
			return keyTarget{pane: p.ID}
		}
		return keyTarget{}
	}
	if a.ck.open {
		if a.ko.reply && a.ck.reply.boxDraft != nil {
			return keyTarget{reply: true}
		}
		return keyTarget{list: true}
	}
	if s := a.session(a.selected); s != nil {
		return keyTarget{pane: a.focusOf(s)}
	}
	return keyTarget{}
}

// targetRef is the element of a target, nil for none.
func (a *App) targetRef(t keyTarget) *ui.Handle {
	switch {
	case t.sheet == "other":
		return nil
	case t.sheet != "":
		return &a.sf.ref
	case t.reply:
		return &a.ck.reply.box
	case t.list:
		return &a.ck.fleetFocus
	case t.pane != "":
		return a.paneRef(t.pane)
	}
	return nil
}

// replyEarly writes what the owner kept into the reply box's draft while
// the sink still has the keys, and keeps the reply box the target.
func (a *App) replyEarly() {
	ko := &a.ko
	ko.mu.Lock()
	q := slices.DeleteFunc(ko.queue, func(k heldKey) bool { return k.slot })
	ko.queue = nil
	ko.mu.Unlock()
	if len(q) == 0 {
		return
	}
	keyAudit("early", "reply", strconv.Itoa(len(q))+" "+a.ck.reply.boxAgent.Name)
	a.replayReply(q)
}

// replayKeys sends what the owner kept to the target, in the order it was
// typed, through the route a key typed there takes.
func (a *App) replayKeys(t keyTarget) {
	ko := &a.ko
	ko.mu.Lock()
	q := slices.DeleteFunc(ko.queue, func(k heldKey) bool { return k.slot })
	ko.queue = nil
	ko.mu.Unlock()
	if t.sheet != "" && len(q) == 0 {
		return // the sheet's field has the keys: nothing to say each frame
	}
	ko.reply = false
	note := strconv.Itoa(len(q))
	if t.reply {
		note += " " + a.ck.reply.boxAgent.Name
	}
	keyAudit("land", t.String(), note)
	switch {
	case t.sheet != "":
		a.replaySheet(t.sheet, q)
	case t.reply:
		a.replayReply(q)
	case t.list:
		a.replayList(q)
	default:
		// No pane, as on a workspace with none: the prefix still takes
		// its keys there, as the prefix card does.
		a.replayPane(t.pane, q)
	}
}

// replayPane sends kept keys to a pane through the router a key typed there
// goes through: the leader and the prefix's keys and text to the prefix
// (keyDown, takeTyped), the rest to the pane. The prefix decides here, on
// the main thread, so the bytes it lets go are sent past it
// (gated.sendPast): a leader later in the queue must not take text that
// came before it (Mac round 3, F3-M1).
//
// A key that runs a prefix action stops the replay: the action runs on a
// later turn of the main thread and can move the keys (a switch, the
// scratch), which must take the rest of the queue. The owner keeps the
// rest until the action ran, then lands it where the keys go then.
func (a *App) replayPane(pane string, q []heldKey) {
	t := a.termFor(pane)
	var g *gated
	if e := a.terms[pane]; e != nil {
		g = e.gate
	}
	send := func(b []byte) {
		switch {
		case t == nil && pane == "":
			keyAudit("drop", string(b), "no pane")
		case t == nil:
			// The pane's stand-in keeps them until its terminal attaches
			// (typeahead.go).
			if len(a.early[pane])+len(b) <= typeaheadMax {
				if a.early == nil {
					a.early = map[string][]byte{}
				}
				a.early[pane] = append(a.early[pane], b...)
			} else {
				keyAudit("drop", string(b), "no terminal")
			}
		case g != nil:
			g.sendPast(t, b)
		default:
			t.Send(b)
		}
	}
	// The bytes of a run go in one write: the daemon queues a few input
	// frames of a stream and refuses the next as busy, so a replay of one
	// frame a key lost keys in the middle of a line.
	var run []byte
	flush := func() {
		if len(run) > 0 {
			send(run)
			run = nil
		}
	}
	defer flush()
	for i, k := range q {
		acting := a.ko.acting
		var took bool
		if k.text != "" {
			took = a.takeTyped([]byte(k.text))
		} else {
			took = a.keyDown(pane, k.mods, k.key)
		}
		if !took {
			if k.text != "" {
				run = append(run, k.text...)
			} else if b := switchKeyBytes(k.mods, k.key); b != nil {
				run = append(run, b...)
			}
			continue
		}
		flush()
		if a.ko.acting > acting && i+1 < len(q) {
			// The key chose an action (actStart): the rest waits for it,
			// and lands where the keys go once it ran (actDone).
			a.ko.mu.Lock()
			a.ko.queue = append(slices.Clone(q[i+1:]), a.ko.queue...)
			a.ko.mu.Unlock()
			return
		}
	}
}

// replayList sends kept keys to the agents list, through its key route
// (listKey). Text goes to the reply box, as typed text in the list does
// (ckProse), with the keys after it.
func (a *App) replayList(q []heldKey) {
	for i, k := range q {
		if k.text == "" {
			if !a.listKey(k.mods, k.key) && !a.chordKey(k.mods, k.key) {
				keyAudit("drop", k.name(), "list")
			}
			continue
		}
		if !a.ckProse(k.text) {
			continue
		}
		// ckProse kept the text: the rest goes after it.
		a.ko.mu.Lock()
		a.ko.queue = append(a.ko.queue, q[i+1:]...)
		a.ko.mu.Unlock()
		return
	}
}

// replayReply sends kept keys to the reply box: text to its end, and the
// keys through the box's own key route (replyKey).
func (a *App) replayReply(q []heldKey) {
	r := &a.ck.reply
	ag, d := r.boxAgent, r.boxDraft
	if d == nil {
		return
	}
	for _, k := range q {
		switch {
		case k.text != "":
			if !d.sending {
				d.text += k.text
			}
		case k.key == ui.KeyBackspace && k.mods == 0:
			if _, n := utf8.DecodeLastRuneInString(d.text); n > 0 {
				d.text = d.text[:len(d.text)-n]
			}
		case k.key == ui.KeyEnter && k.mods == ui.Shift:
			d.text += "\n"
		default:
			text := d.text
			if took, set := a.replyKey(ag, d, r.boxMail, k.mods, k.key, &text); took {
				if set {
					d.text = text
				}
			} else {
				keyAudit("drop", k.name(), "reply")
			}
		}
	}
	if len(q) > 0 {
		// Only with text written here: the caret goes after it, by a
		// draft that holds no text typed in the box since (that frame
		// moved the caret back into the typing).
		r.toEnd = true
	}
}

// keyText is the text a key that types text types on a US layout, for a
// key the owner took before the terminal saw its text.
func keyText(mods ui.Modifiers, key ui.Key) string {
	shift := mods&ui.Shift != 0
	switch {
	case key == ui.KeySpace:
		return " "
	case key >= ui.KeyA && key <= ui.KeyZ:
		r := rune('a' + int(key-ui.KeyA))
		if shift {
			r -= 'a' - 'A'
		}
		return string(r)
	}
	c := usChars[key]
	if shift {
		return c[1]
	}
	return c[0]
}

// auditCaller names the code that asked for a hold, for the key audit.
var auditCaller = func() string { return "" }

// keyAudit notes what the owner does, with GUIOS_KEY_AUDIT set in a
// build for checks (automation_e2e.go): hold, keep, done, land and drop.
var keyAudit = func(ev, what, note string) {}

// switchKeyBytes are the bytes a key that types no text sends, in the
// plain encoding every shell reads.
func switchKeyBytes(mods ui.Modifiers, key ui.Key) []byte {
	name, ok := keyString(mods, key)
	if !ok {
		return nil
	}
	switch name {
	case "enter":
		return []byte{'\r'}
	case "tab":
		return []byte{'\t'}
	case "esc":
		return []byte{0x1b}
	case "backspace":
		return []byte{0x7f}
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "home":
		return []byte("\x1b[H")
	case "end":
		return []byte("\x1b[F")
	case "delete":
		return []byte("\x1b[3~")
	}
	return keyBytes(name)
}

// name spells a kept input for the key audit.
func (k heldKey) name() string {
	if k.text != "" {
		return k.text
	}
	if s, ok := keyString(k.mods, k.key); ok {
		return s
	}
	return "?"
}

// String spells a target for the key audit.
func (t keyTarget) String() string {
	switch {
	case t.reply:
		return "reply"
	case t.list:
		return "list"
	case t.pane != "":
		return "pane " + t.pane
	case t.sheet != "":
		return "sheet " + t.sheet
	}
	return "none"
}

// auditFocus notes, for the key audit, which element has the keys when it
// changes: the sink, the reply box, the list, a pane, or none of them.
func (a *App) auditFocus(c *ui.Context) {
	at := "other"
	switch {
	case a.ko.ref.Focused(c):
		at = "sink"
	case a.ck.reply.box.FocusWithin(c):
		at = "reply"
	case a.ck.fleetFocus.FocusWithin(c):
		at = "list"
	default:
		for id := range a.terms {
			if a.paneRef(id).FocusWithin(c) {
				at = "pane " + id
			}
		}
	}
	if at != a.ko.seen {
		a.ko.seen = at
		keyAudit("focus", at, "")
	}
}

// seqNote is the key audit's note of press seq.
func seqNote(seq uint64) string {
	if seq == 0 {
		return ""
	}
	return "seq " + strconv.FormatUint(seq, 10)
}

// ownerAsked reports whether the owner asked the focus for pane while it
// lands. The focus moves a frame after the ask, so the pane that has it
// then may be one the owner asked for before its target changed: taking it
// as the user's choice would move the target back, and the two would swap
// the focus each frame.
func (a *App) ownerAsked(pane string) bool {
	return a.ko.phase == koLanding && slices.Contains(a.ko.asked, pane)
}
