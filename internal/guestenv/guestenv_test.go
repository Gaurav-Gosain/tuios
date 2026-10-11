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

// TestWithoutHostMultiplexerDropsOuterTernPane covers a daemon started from a
// Tern pane. Tern names its pane and window in TERN_ variables and, as it
// answers XTVERSION as kitty, sets kitty's KITTY_WINDOW_ID and KITTY_PID. A
// tuios pane that inherits them sends `tern open`, Tern's agent verbs and
// `kitty @` to the Tern pane the daemon happened to start in. The variables
// that name Tern's daemon and folders say nothing about a pane, so they stay.
func TestWithoutHostMultiplexerDropsOuterTernPane(t *testing.T) {
	env := []string{
		"TERN_PANE=7", "TERN_PANE_SOCKET=/run/tern/pane.sock", "TERN_WINDOW_KEY=k",
		"TERN_WINDOW_SOCKET=/tmp/tern/open.sock", "TERN_WINDOW_PANE=2", "TERN_IDENTITY=id",
		"TERN_LENSES=1", "TERN_COMPLETE=1", "TERN_ACP=",
		"KITTY_WINDOW_ID=1", "KITTY_PID=4242",
		"TERN_DAEMON_SOCKET=/run/tern/daemon.sock", "TERN_CONFIG_DIR=/home/u/.config/tern",
		"TERN_BLOB_DIR=/home/u/.cache/tern/blobs", "TERM_PROGRAM=tern",
	}
	got := WithoutHostMultiplexer(slices.Clone(env))
	want := []string{
		"TERN_DAEMON_SOCKET=/run/tern/daemon.sock", "TERN_CONFIG_DIR=/home/u/.config/tern",
		"TERN_BLOB_DIR=/home/u/.cache/tern/blobs", "TERM_PROGRAM=tern",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
