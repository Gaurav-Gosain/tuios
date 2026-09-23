package session

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// recordingPane is a promptPane that keeps every write, in order, with the time
// it arrived. echo, when set, makes it look like an application that prints
// something in answer to each write.
type recordingPane struct {
	mu        sync.Mutex
	writes    []string
	at        []time.Time
	bracketed bool
	focus     bool
	echo      bool
	last      atomic.Int64
}

func (r *recordingPane) FocusReportingOn() bool { return r.focus }

func (r *recordingPane) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.writes = append(r.writes, string(b))
	r.at = append(r.at, time.Now())
	r.mu.Unlock()
	if r.echo {
		r.last.Store(time.Now().UnixNano())
	}
	return len(b), nil
}

func (r *recordingPane) BracketedPasteOn() bool { return r.bracketed }
func (r *recordingPane) LastOutput() int64      { return r.last.Load() }

func (r *recordingPane) recorded() ([]string, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.writes...), append([]time.Time(nil), r.at...)
}

// TestSubmitPromptWritesExactBytes pins the bytes a prompt becomes: the text,
// wrapped in the bracketed paste delimiters when the pane asked for them, then
// a carriage return on its own write. Never a line feed to submit, since
// several agent TUIs read a line feed as "insert a newline".
func TestSubmitPromptWritesExactBytes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bracketed bool
		text      string
		want      []string
	}{
		{"bracketed", true, "does the retry path look right?", []string{"\x1b[200~does the retry path look right?\x1b[201~", "\r"}},
		{"raw", false, "does the retry path look right?", []string{"does the retry path look right?", "\r"}},
		{"trailing newline dropped", true, "hello\n", []string{"\x1b[200~hello\x1b[201~", "\r"}},
		{"trailing crlf dropped", false, "hello\r\n", []string{"hello", "\r"}},
		{"paste end in the text cannot close the paste", true, "a\x1b[201~b\x1b[200~c", []string{"\x1b[200~abc\x1b[201~", "\r"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pane := &recordingPane{bracketed: tc.bracketed}
			if err := submitPromptTimed(context.Background(), pane, tc.text, harness.DefaultInputProfile(), 5*time.Millisecond, 20*time.Millisecond); err != nil {
				t.Fatalf("submitPrompt: %v", err)
			}
			got, _ := pane.recorded()
			if len(got) != len(tc.want) {
				t.Fatalf("writes = %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("write %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSubmitPromptSubmitsMultiLineTextOnce covers a prompt of several lines.
// Typed as raw lines it was a sequence of Enters, and an agent that submits on
// each sent the first line alone. As a paste it is one input, and the only
// submitting byte is the one carriage return at the end.
func TestSubmitPromptSubmitsMultiLineTextOnce(t *testing.T) {
	pane := &recordingPane{bracketed: true}
	text := "review this diff\r\nfocus on the retry path\nand the timeout\n\n"
	if err := submitPromptTimed(context.Background(), pane, text, harness.DefaultInputProfile(), 5*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatalf("submitPrompt: %v", err)
	}
	got, _ := pane.recorded()
	all := strings.Join(got, "")
	if n := strings.Count(all, "\r"); n != 1 {
		t.Errorf("the prompt carried %d carriage returns, want exactly one: %q", n, all)
	}
	if !strings.HasSuffix(all, "\x1b[201~\r") {
		t.Errorf("the carriage return is not the last byte after the paste: %q", all)
	}
	want := "\x1b[200~review this diff\nfocus on the retry path\nand the timeout\x1b[201~"
	if got[0] != want {
		t.Errorf("paste = %q, want %q", got[0], want)
	}
}

// TestSubmitPromptWaitsBeforeTheCarriageReturn covers the gap between the paste
// and the Enter. A pane that prints nothing gets the whole wait, since silence
// says nothing about whether it has read the paste. A pane that echoes the
// paste and goes quiet is submitted as soon as it has been quiet.
func TestSubmitPromptWaitsBeforeTheCarriageReturn(t *testing.T) {
	const quiet, maxWait = 20 * time.Millisecond, 250 * time.Millisecond

	silent := &recordingPane{bracketed: true}
	if err := submitPromptTimed(context.Background(), silent, "hi", harness.DefaultInputProfile(), quiet, maxWait); err != nil {
		t.Fatal(err)
	}
	_, at := silent.recorded()
	if gap := at[1].Sub(at[0]); gap < maxWait {
		t.Errorf("a silent pane was submitted %v after the paste, want at least %v", gap, maxWait)
	}

	echoing := &recordingPane{bracketed: true, echo: true}
	if err := submitPromptTimed(context.Background(), echoing, "hi", harness.DefaultInputProfile(), quiet, maxWait); err != nil {
		t.Fatal(err)
	}
	_, at = echoing.recorded()
	if gap := at[1].Sub(at[0]); gap < quiet || gap >= maxWait {
		t.Errorf("an echoing pane was submitted %v after the paste, want between %v and %v", gap, quiet, maxWait)
	}
}

// TestSubmitPromptStopsWithTheDaemon: a daemon shutting down does not finish
// typing, and says the prompt was not submitted.
func TestSubmitPromptStopsWithTheDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pane := &recordingPane{}
	if err := submitPromptTimed(ctx, pane, "hi", harness.DefaultInputProfile(), time.Second, time.Second); err == nil {
		t.Fatal("a cancelled submit reported success")
	}
	if got, _ := pane.recorded(); len(got) != 1 {
		t.Errorf("writes = %q, want only the paste", got)
	}
}

// TestSubmitPromptFollowsTheInputProfile pins what a harness's [input] block
// changes: the submit key, whether a paste may be bracketed, and a focus-in
// report ahead of the prompt, sent only to a pane that asked for focus events.
func TestSubmitPromptFollowsTheInputProfile(t *testing.T) {
	def := harness.DefaultInputProfile()
	lf := def
	lf.SubmitKey = "\n"
	noPaste := def
	noPaste.BracketedPaste = false
	focus := def
	focus.FocusBeforeSubmit = true
	for _, tc := range []struct {
		name      string
		in        harness.InputProfile
		bracketed bool
		focusOn   bool
		want      []string
	}{
		{"line feed submit", lf, true, false, []string{"\x1b[200~hi\x1b[201~", "\n"}},
		{"paste refused although the mode is on", noPaste, true, false, []string{"hi", "\r"}},
		{"focus report first", focus, true, true, []string{"\x1b[I", "\x1b[200~hi\x1b[201~", "\r"}},
		{"no focus report to a pane that did not ask", focus, true, false, []string{"\x1b[200~hi\x1b[201~", "\r"}},
		{"zero profile submits with a carriage return", harness.InputProfile{}, false, false, []string{"hi", "\r"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pane := &recordingPane{bracketed: tc.bracketed, focus: tc.focusOn}
			if err := submitPromptTimed(context.Background(), pane, "hi", tc.in, time.Millisecond, 5*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			got, _ := pane.recorded()
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("writes = %q, want %q", got, tc.want)
			}
		})
	}
}
