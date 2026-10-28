package app

import (
	"context"
	"io"
	"reflect"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// The input boundary of #1269. On Windows, ultraviolet's console reader turns
// ConPTY's records for an uppercase U (Shift down with character 0, U down, U
// up, Shift up) into the bytes "\x00U". This feeds those bytes through the
// same reader and decoder bubbletea runs, then through the program filter the
// way bubbletea's event loop does, and reads the keys that reach Update.
//
// The e2e suite cannot run ConPTY, so this holds the boundary instead.
func TestConPTYShiftNULIsNotCtrlSpace(t *testing.T) {
	prev := conptyInput
	conptyInput = true
	t.Cleanup(func() { conptyInput = prev })

	for _, tc := range []struct {
		what   string
		writes []string
		want   []string
	}{
		// The report: the leader, then Shift+U. The phantom Ctrl+Space closed
		// the which-key menu before the U arrived.
		{"leader then shift+u", []string{"\x02", "\x00U"}, []string{"ctrl+b", "U"}},
		// Caps Lock and a shifted symbol take the same path.
		{"shifted symbol", []string{"\x00!"}, []string{"!"}},
		// A real Ctrl+Space is one NUL on its own, and it still arrives.
		{"ctrl+space", []string{"\x00"}, []string{"ctrl+space"}},
		// A real Ctrl+Space followed in the same read by a key with no text
		// keeps both, in order.
		{"ctrl+space then up", []string{"\x00\x1b[A"}, []string{"ctrl+space", "up"}},
		// Two real Ctrl+Space presses some time apart are both kept.
		{"ctrl+space twice", []string{"\x00", "\x00"}, []string{"ctrl+space", "ctrl+space"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got := keysThroughFilter(t, filterOS(t), tc.writes)
			if !slices.Equal(got, tc.want) {
				t.Errorf("keys that reach Update: %q, want %q", got, tc.want)
			}
		})
	}
}

// keysThroughFilter writes each chunk to a ultraviolet terminal reader as one
// read, 100 ms apart, and runs what it decodes through FilterMouseMotion with
// a small copy of bubbletea's event loop: a batch runs its commands at once,
// a sequence runs them in order, and every result goes back through the
// filter. It returns the key presses the filter passes on, as strings.
func keysThroughFilter(t *testing.T, o *OS, writes []string) []string {
	t.Helper()
	pr, pw := io.Pipe()
	reader := uv.NewTerminalReader(pr, "xterm-256color")
	events := make(chan uv.Event, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = reader.StreamEvents(ctx, events) }()
	go func() {
		for _, w := range writes {
			_, _ = pw.Write([]byte(w))
			time.Sleep(100 * time.Millisecond)
		}
		_ = pw.Close()
	}()

	msgs := make(chan tea.Msg, 64)
	go func() {
		for ev := range events {
			switch e := ev.(type) {
			case uv.KeyPressEvent:
				msgs <- tea.KeyPressMsg(e)
			case uv.KeyReleaseEvent:
				msgs <- tea.KeyReleaseMsg(e)
			}
		}
	}()
	run := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		if msg := cmd(); msg != nil {
			msgs <- msg
		}
	}

	var got []string
	quiet := time.Duration(len(writes)+2) * 100 * time.Millisecond
	deadline := time.After(quiet)
	for {
		select {
		case msg := <-msgs:
			msg = FilterMouseMotion(o, msg)
			switch m := msg.(type) {
			case nil:
			case tea.KeyPressMsg:
				got = append(got, m.String())
			case tea.BatchMsg:
				for _, cmd := range m {
					go run(cmd)
				}
			default:
				// bubbletea's sequenceMsg is unexported: a slice of commands.
				if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
					cmds := make([]tea.Cmd, v.Len())
					for i := range cmds {
						cmds[i] = v.Index(i).Interface().(tea.Cmd)
					}
					go func() {
						for _, cmd := range cmds {
							run(cmd)
						}
					}()
				}
			}
		case <-deadline:
			return got
		}
	}
}
