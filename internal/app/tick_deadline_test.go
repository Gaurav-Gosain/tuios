package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestArmTickCancelsSupersededCommand(t *testing.T) {
	m := &OS{}
	t.Cleanup(m.signalStop)

	first := m.armTick(time.Hour)
	firstDone := make(chan tea.Msg, 1)
	go func() {
		firstDone <- first()
	}()

	second := m.armTick(10 * time.Millisecond)

	select {
	case msg := <-firstDone:
		if msg != nil {
			t.Fatalf("superseded tick returned %T (%v), want nil", msg, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("superseded tickUntilStop goroutine did not exit after armTick replaced it")
	}

	msg := second()
	dt, ok := msg.(deadlineTickMsg)
	if !ok {
		t.Fatalf("active tick returned %T (%v), want deadlineTickMsg", msg, msg)
	}
	if dt.gen != m.tick.gen {
		t.Fatalf("active tick gen = %d, want %d", dt.gen, m.tick.gen)
	}

	m.Update(dt)
	if m.tick.cancel != nil || !m.tick.due.IsZero() {
		t.Fatalf("after handling deadlineTickMsg, tick state not cleared: due=%v cancel=%v", m.tick.due, m.tick.cancel != nil)
	}
}

func TestArmTickStopSignalCancelsActiveCommand(t *testing.T) {
	m := &OS{}

	cmd := m.armTick(time.Hour)
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()

	m.signalStop()

	select {
	case msg := <-done:
		if msg != nil {
			t.Fatalf("stopped tick returned %T (%v), want nil", msg, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("tickUntilStop goroutine did not exit after signalStop")
	}
}
