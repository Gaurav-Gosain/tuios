package session

import (
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

const askTranscriptEntries = 200

const askTranscriptWait = 2 * time.Second

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

type askMark struct {
	joined bool
	cursor string
}

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

func lastIndexFunc[T any](s []T, f func(T) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if f(s[i]) {
			return i
		}
	}
	return -1
}
