package app

import "testing"

func quitFlagsOS(t *testing.T) *OS {
	t.Helper()
	m := newTestOS(newTestWindow(t, "quit-flags-0001", 60, 20))
	m.Mode = TerminalMode
	return m
}

func TestQuitCmdKeepsTheKeyboardFlagsOfTheLastFrame(t *testing.T) {
	m := quitFlagsOS(t)
	resting := m.keyboardEnhancements()
	m.ShowQuitMenu = true
	menu := m.keyboardEnhancements()
	if menu == resting {
		t.Fatal("setup: the quit menu being open must change what the view asks the host for")
	}
	m.ShowQuitMenu = false
	if got := m.keyboardEnhancements(); got != resting {
		t.Fatalf("setup: closing the menu on a client that is not quitting must change the flags back, got %+v want %+v", got, resting)
	}

	m.ShowQuitMenu = true
	if got := m.keyboardEnhancements(); got != menu {
		t.Fatalf("setup: the frame drawn with the menu open asks for %+v, want %+v", got, menu)
	}
	m.ShowQuitMenu = false
	if cmd := m.QuitCmd(); cmd == nil {
		t.Fatal("QuitCmd returned no command")
	}
	if got := m.keyboardEnhancements(); got != menu {
		t.Errorf("after the quit the view asks for %+v, want the last frame's %+v, so no query is sent", got, menu)
	}
}
