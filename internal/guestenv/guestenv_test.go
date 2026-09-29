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
		"TUIOS_RESTORED=1", "TUIOS_SESSION_REMOTE=r", "TUIOS_PANE_HOSTED=1",
		"TUIOS_ENV=1", "TMUX=/tmp/tmux",
	}
	got := WithoutHostMultiplexer(slices.Clone(env))
	want := []string{"HOME=/home/u", "TUIOS_ENV=1"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
