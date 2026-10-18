package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The maintenance tick runs only when a job is due.
//
// It used to run for the life of the client: at the frame rate while
// something moved, and at IdleFPS otherwise, where its idle gate
// (tickNeedsWork) found nothing to do ten times a second. Each of those ticks
// woke four goroutines, which was most of an idle client's context switches.
//
// Now Update asks nextWake after every message when the next job is due, and
// arms one tick for that moment. A job is due on its own clock (a message's
// burn, an alert held back, the zen reveal running out, the frame ticker's
// idle switch, the git section's next reading), or because the idle gate
// would find work now, which it checks at the old idle rate. With nothing due
// no tick is armed, and the client sleeps until a message arrives.
//
// Every state the idle gate polls changes on the Update goroutine, in the
// handling of some message, so asking after every message sees it. The
// exceptions are set by other goroutines, and each of them is followed by a
// message: pane output and a title by PTYDataMsg, a moved listing by
// foreignSessionsChangedMsg, a process exit by WindowExitMsg. A local exit
// whose send found WindowExitChan full is the one that is not, and it still
// has the messages queued ahead of it, each of which looks.

// tickTimer is the one maintenance tick armed, if any.
type tickTimer struct {
	// gen is the generation of the tick armed last. A tick of an older one
	// was replaced by an earlier one and is dropped.
	gen uint64
	// due is when the tick armed last fires, zero when none is armed.
	due time.Time
}

// deadlineTickMsg is a maintenance tick armed by armTick. Update hands it on
// as a TickerMsg when it is the one armed last.
type deadlineTickMsg struct {
	gen uint64
	at  time.Time
}

// tickPollPeriod is how soon the tick looks again at work the idle gate finds
// by looking: the rate the idle tick ran at.
var tickPollPeriod = time.Second / time.Duration(config.IdleFPS)

// armTick arms the maintenance tick wait from now, and replaces any tick armed
// before it.
func (m *OS) armTick(wait time.Duration) tea.Cmd {
	wait = max(wait, time.Millisecond)
	m.tick.gen++
	m.tick.due = time.Now().Add(wait)
	gen := m.tick.gen
	return tea.Tick(wait, func(t time.Time) tea.Msg { return deadlineTickMsg{gen: gen, at: t} })
}

// tickWakeCmd arms the tick for the next job that is due, unless a tick armed
// at or before then is already on its way. Update calls it after every
// message.
func (m *OS) tickWakeCmd() tea.Cmd {
	now := time.Now()
	due, ok := m.nextWake(now)
	if !ok {
		return nil
	}
	if !m.tick.due.IsZero() && !due.Before(m.tick.due) {
		return nil
	}
	return m.armTick(due.Sub(now))
}

// frameIdleTickCmd arms the tick for the frame ticker's idle switch, unless
// one armed at or before then is already on its way. Update calls it for
// flushMsg.
//
// View wakes the frame ticker (noteFrame) after Update has asked nextWake,
// so a message that finds the ticker stopped and composes a frame leaves it
// running at the frame rate with no tick armed to stop it. Before this, it ran
// until some later message armed one: up to the 30 s listing poll with the
// rail hidden, and all the time with the git section, whose reading every 2 s
// woke it again as the tick for the next one stopped it. The flushMsg Update
// asks for after such a frame comes once View has run.
func (m *OS) frameIdleTickCmd() tea.Cmd {
	fr := &m.frameRate
	if fr.program == nil || fr.idle {
		return nil
	}
	due := fr.lastFrame.Add(idleTickerAfter)
	if !m.tick.due.IsZero() && !due.Before(m.tick.due) {
		return nil
	}
	return m.armTick(time.Until(due))
}

// nextWake is when the maintenance tick has to run next, and false when
// nothing is due and the client may sleep until a message arrives.
func (m *OS) nextWake(now time.Time) (time.Time, bool) {
	if m.tickAtFrameRate() {
		return now.Add(m.framePeriod()), true
	}
	var due time.Time
	at := func(t time.Time) {
		if due.IsZero() || t.Before(due) {
			due = t
		}
	}
	// The message burn, which knows when its next cell goes out.
	if len(m.Notifications) > 0 {
		if wait := m.notifTickWait(now); wait != notifNoMove {
			at(now.Add(wait))
		}
	}
	// An agent row held in its group a moment, so a quick flip of state does
	// not move it twice, moves when the hold ends.
	if m.ShowInbox && !m.Inbox.ck.holdUntil.IsZero() {
		at(m.Inbox.ck.holdUntil)
	}
	// A pointer move held back by the frame budget goes out on the next frame.
	if m.spotlightMotionPending {
		at(now.Add(m.framePeriod()))
	}
	// What the idle gate finds by looking.
	if m.tickPolledWork() {
		at(now.Add(tickPollPeriod))
	}
	// Alerts held back go out at their due time.
	for _, p := range m.pendingAgentAlerts {
		at(p.due)
	}
	for _, pa := range m.programAlerts {
		if pa.pending != nil {
			at(pa.pending.due)
		}
	}
	// The zen reveal and a gesture's announcement hold run out with no event.
	if m.Settings.ZenMode == config.ZenModeMouse && m.pointerRecentlyMoved() {
		at(m.lastPointerAt.Add(zenModeMouseIdleTimeout + time.Millisecond))
	}
	if m.announceGestureHeld && m.pointerDown && !m.lastPointerAt.IsZero() {
		at(m.lastPointerAt.Add(announceHoldTimeout + time.Millisecond))
	}
	// The panes' spare emulator buffers, once after a frame changed.
	if t, ok := m.emulatorReleaseDue(); ok {
		at(t)
	}
	// The frame ticker stops idleTickerAfter after the last frame.
	if fr := &m.frameRate; fr.program != nil && !fr.idle {
		at(fr.lastFrame.Add(idleTickerAfter))
	}
	// The git section asks again on its interval, and the files section
	// retries a listing that failed. Each rail has its own.
	m.railReadsDue(at)
	if m.secondarySidebarWidth() > 0 {
		m.withSecondaryRail(func() bool {
			m.railReadsDue(at)
			return true
		})
	}
	if due.IsZero() {
		return due, false
	}
	// A due already past is a job the last tick did not clear. It is looked
	// at again at the old idle rate, never in a loop.
	if !due.After(now) {
		due = now.Add(tickPollPeriod)
	}
	return due, true
}

// railReadsDue reports to at when the rail being drawn next reads its git
// state and retries its file listing. GitSyncCmd and FilesSyncCmd ask on the
// message after that moment.
func (m *OS) railReadsDue(at func(time.Time)) {
	if m.gitView.asked != "" && m.gitSectionEnabled() {
		at(m.gitView.at.Add(gitRefreshInterval))
	}
	if v := m.filesView; v.Err != "" && !v.Loading && m.filesSectionEnabled() && v.Want == m.filesWantDir() {
		at(v.ErrAt.Add(fileRetryInterval))
	}
}
