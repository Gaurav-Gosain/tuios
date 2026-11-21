package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type tickTimer struct {
	gen uint64
	due time.Time
}

type deadlineTickMsg struct {
	gen uint64
	at  time.Time
}

func (m *OS) tickPollPeriod() time.Duration {
	fps := m.Settings.IdleFPS
	if fps <= 0 {
		fps = config.IdleFPSDefault
	}
	return time.Second / time.Duration(fps)
}

func (m *OS) armTick(wait time.Duration) tea.Cmd {
	wait = max(wait, time.Millisecond)
	m.tick.gen++
	m.tick.due = time.Now().Add(wait)
	gen := m.tick.gen
	return m.tickUntilStop(wait, func(t time.Time) tea.Msg { return deadlineTickMsg{gen: gen, at: t} })
}

func (m *OS) tickUntilStop(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	timer := time.NewTimer(d)
	stop := m.stopSignal()
	return func() tea.Msg {
		select {
		case t := <-timer.C:
			return fn(t)
		case <-stop:
			timer.Stop()
			return nil
		}
	}
}

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
	if len(m.Notifications) > 0 {
		if wait := m.notifTickWait(now); wait != notifNoMove {
			at(now.Add(wait))
		}
	}
	if m.ShowInbox && !m.Inbox.ck.holdUntil.IsZero() {
		at(m.Inbox.ck.holdUntil)
	}
	if m.pointerMotionPending {
		at(m.motionBudgetFrom().Add(m.framePeriod()))
	}
	if m.tickPolledWork() {
		at(now.Add(m.tickPollPeriod()))
	}
	for _, p := range m.pendingAgentAlerts {
		at(p.due)
	}
	for _, pa := range m.programAlerts {
		if pa.pending != nil {
			at(pa.pending.due)
		}
	}
	if m.Settings.ZenMode == config.ZenModeMouse && m.pointerRecentlyMoved() {
		at(m.lastPointerAt.Add(m.Settings.ZenReveal() + time.Millisecond))
	}
	if m.announceGestureHeld && m.pointerDown && !m.lastPointerAt.IsZero() {
		at(m.lastPointerAt.Add(announceHoldTimeout + time.Millisecond))
	}
	if t, ok := m.emulatorReleaseDue(); ok {
		at(t)
	}
	if fr := &m.frameRate; fr.program != nil && !fr.idle {
		at(fr.lastFrame.Add(idleTickerAfter))
	}
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
	if !due.After(now) {
		due = now.Add(m.tickPollPeriod())
	}
	return due, true
}

func (m *OS) railReadsDue(at func(time.Time)) {
	if m.gitView.asked != "" && m.gitSectionEnabled() {
		at(m.gitView.at.Add(m.gitRefreshInterval()))
	}
	if v := m.filesView; v.Err != "" && !v.Loading && m.filesSectionEnabled() && v.Want == m.filesWantDir() {
		at(v.ErrAt.Add(fileRetryInterval))
	}
}

// PointerMotionPending reports whether a pointer move is waiting for the
// maintenance tick to draw it, because it arrived inside the frame budget of
// the last frame. See the motion budget in update.go.
func (m *OS) PointerMotionPending() bool {
	return m.pointerMotionPending
}
