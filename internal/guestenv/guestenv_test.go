package guestenv

import "testing"

func TestWithoutHostMultiplexer(t *testing.T) {
	env := []string{
		"HOME=/home/me",
		"TMUX=/tmp/tmux-1000/default,1234,0",
		"TMUX_PANE=%3",
		"TMUXINATOR_CONFIG=/home/me/.tmuxinator",
		"TMUX_TMPDIR=/tmp",
		"TERM=screen-256color",
	}
	got := WithoutHostMultiplexer(env)
	want := []string{
		"HOME=/home/me",
		"TMUXINATOR_CONFIG=/home/me/.tmuxinator",
		"TMUX_TMPDIR=/tmp",
		"TERM=screen-256color",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}
