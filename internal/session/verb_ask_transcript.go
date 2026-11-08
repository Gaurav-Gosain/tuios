package session

import (
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// The reply of an ask from the agent's own transcript.
//
// ask-agent reads the reply from the pane: what it printed after the question.
// An agent that draws its whole screen again for each turn, as Claude Code
// does, leaves as many lines after its answer as before the question, and that
// reply is empty although the agent answered. For a pane joined to a
// transcript this daemon reads, the reply is the agent's text since the
// question, read the way agent-transcript reads it (the same cleaning and
// secret masking). The screen stays the reply of every other pane.

// askTranscriptEntries bounds the entries an ask reads after its question.
const askTranscriptEntries = 200

// askTranscriptWait is how long an ask waits for the agent's text to reach its
// transcript after the agent settled: a harness can report the end of its turn
// before its last write.
const askTranscriptWait = 2 * time.Second

// transcriptOptions is where window's transcript is and how to read it, when
// the pane is joined to one in a format this daemon reads.
func (d *Daemon) transcriptOptions(sess *Session, window string) (string, transcriptview.Options, bool) {
	reg := d.agentMatcher.registry
	path, harnessID, format, session, leaf, joined := sess.transcriptViewOf(window)
	if !joined || reg == nil {
		return "", transcriptview.Options{}, false
	}
	if tr := reg.TranscriptFor(harnessID); tr == nil || tr.Reader != format || !transcriptview.Known(format) {
		return "", transcriptview.Options{}, false
	}
	return path, transcriptview.Options{
		Clean:      transcriptText,
		CleanLines: maskSecretLines,
		Format:     format,
		Session:    session,
		Leaf:       leaf,
	}, true
}

// askMark is where window's transcript ended before the question.
type askMark struct {
	// joined says the pane has a transcript this daemon reads.
	joined bool
	// cursor is its end, "" when the file was empty or not there yet: a
	// fresh agent writes its transcript with its first turn, so everything
	// in it then came after the question.
	cursor string
}

// askTranscriptMark is the mark of window's transcript before the question.
func (d *Daemon) askTranscriptMark(sess *Session, window string) askMark {
	path, opts, ok := d.transcriptOptions(sess, window)
	if !ok {
		return askMark{}
	}
	opts.Limit = 1
	page, err := transcriptview.Read(path, opts)
	if err != nil || len(page.Entries) == 0 {
		return askMark{joined: true}
	}
	return askMark{joined: true, cursor: page.Cursor}
}

// askTranscriptReply is the agent's text after the question, from the entries
// after the mark: the assistant's text after the first prompt there, which is
// the question. A file that was empty at the mark, or changed under it (a new
// session), gives the text after its last prompt. It waits up to
// askTranscriptWait for the text.
func (d *Daemon) askTranscriptReply(sess *Session, window string, mark askMark) (string, bool) {
	if !mark.joined {
		return "", false
	}
	deadline := time.Now().Add(askTranscriptWait)
	for {
		if text, ok := d.readAskReply(sess, window, mark.cursor); ok {
			return text, true
		}
		if time.Now().After(deadline) || d.ctx.Err() != nil {
			return "", false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) readAskReply(sess *Session, window, cursor string) (string, bool) {
	path, opts, ok := d.transcriptOptions(sess, window)
	if !ok {
		return "", false
	}
	opts.After, opts.Limit = cursor, askTranscriptEntries
	page, err := transcriptview.Read(path, opts)
	if err != nil {
		return "", false
	}
	entries := page.Entries
	isPrompt := func(e transcriptview.Entry) bool {
		return e.Role == transcriptview.RoleUser && e.Kind == transcriptview.KindText
	}
	if page.Reset || cursor == "" {
		if i := lastIndexFunc(entries, isPrompt); i >= 0 {
			entries = entries[i+1:]
		}
	} else if i := slices.IndexFunc(entries, isPrompt); i >= 0 {
		entries = entries[i+1:]
	}
	return assistantText(entries)
}

// assistantText joins the assistant's text entries, in order.
func assistantText(entries []transcriptview.Entry) (string, bool) {
	var parts []string
	for _, e := range entries {
		if e.Role == transcriptview.RoleAssistant && e.Kind == transcriptview.KindText && strings.TrimSpace(e.Text) != "" {
			parts = append(parts, e.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}

// lastIndexFunc is the index of the last element of s that f accepts, or -1.
func lastIndexFunc[T any](s []T, f func(T) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if f(s[i]) {
			return i
		}
	}
	return -1
}
