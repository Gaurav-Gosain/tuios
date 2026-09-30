package guestenv

import (
	"slices"
	"testing"
)

// TestWithoutHostMultiplexerDropsOuterTuiosPane covers a daemon started from a
// tuios pane: its panes must not inherit the outer pane's session, socket and
// ids, which would place them in the outer session.
func TestWithoutHostMultiplexerDropsOuterTuiosPane(t *testing.T) {
	env := []string{
		"HOME=/home/u",
		"TUIOS_SESSION=outer", "TUIOS_SOCKET=/run/outer.sock", "TUIOS_PANE_ID=w1",
		"TUIOS_WINDOW_ID=w1", "TUIOS_PANE_TOKEN=t", "TUIOS_PANE_GRANTS=g",
		"TUIOS_RESTORED=1", "TUIOS_PANE_TTY=/dev/pts/9", "TUIOS_SESSION_REMOTE=r", "TUIOS_PANE_HOSTED=1",
		"TUIOS_ENV=1", "TMUX=/tmp/tmux",
	}
	got := WithoutHostMultiplexer(slices.Clone(env))
	want := []string{"HOME=/home/u", "TUIOS_ENV=1"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestWithoutHostMultiplexerDropsOuterHerdrBin: a tuios started in a pane of
// another tuios inherits HERDR_BIN_PATH naming the outer one, whose report
// commands would report to the outer pane. It goes with the pane ids, and a
// pane of this tuios gets its own again when herdr_protocol asks for it.
// HERDR_SOCKET_PATH stays, as for herdr.
func TestWithoutHostMultiplexerDropsOuterHerdrBin(t *testing.T) {
	env := []string{"HERDR_ENV=1", "HERDR_PANE_ID=w1", "HERDR_BIN_PATH=/usr/bin/tuios", "HERDR_SOCKET_PATH=/run/outer.sock.herdr"}
	got := WithoutHostMultiplexer(slices.Clone(env))
	if want := []string{"HERDR_SOCKET_PATH=/run/outer.sock.herdr"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
