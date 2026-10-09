package session

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
	"github.com/charmbracelet/x/ansi"
)

// agent-transcript: the agent's conversation, for the person.
//
// The transcript a pane is joined to (agent_transcript.go) holds the whole
// conversation: the prompts, the file contents the agent read and the output
// of every command it ran. Until this verb the daemon read only a turn state
// out of it. This verb reads the content, so it is held to the strictest rule
// the daemon has: only the person may call it. That is the reply-approval
// check, a live human_nonce that verifies for this caller, so a process in a
// pane and a link stream the hub did not vouch for get not_human. A
// restricted connection is automation and is refused outright (conn_scope.go),
// and a link needs respond (link_policy.go), the capability that answers for
// the person.
//
// The path comes from the join, in daemon memory. The caller names a window,
// never a file. The decoding is internal/transcriptview, which nothing else
// calls.

// verbAgentTranscript reads a page of a pane's conversation.
func (d *Daemon) verbAgentTranscript(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		HumanNonce string `json:"human_nonce"`
		After      string `json:"after"`
		Before     string `json:"before"`
		Limit      int    `json:"limit"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is required")
	}
	if p.After != "" && p.Before != "" {
		return nil, invalidParam("before", "give after or before, not both")
	}
	if p.Limit < 0 || p.Limit > transcriptview.MaxLimit {
		return nil, invalidParam("limit", "limit must be between 1 and "+strconv.Itoa(transcriptview.MaxLimit))
	}
	if _, ok := d.humanNonceClient(p.HumanNonce, cs); !ok {
		return nil, hintedVerbError(ErrVerbNotHuman, "agent-transcript is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was read. Only a client attached now, or a connection that holds a presence, can read a transcript, by passing its nonce. An agent never can.",
		})
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	idx, err := findWindowStateIndex(st.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := st.Windows[idx]
	reg := d.agentMatcher.registry
	path, harnessID, joined := sess.transcriptJoinOf(w.ID)
	if !joined {
		if w.AgentHarness != "" && (reg == nil || reg.TranscriptFor(w.AgentHarness) == nil) {
			return nil, unsupportedHarnessError(w.AgentHarness)
		}
		return nil, hintedVerbError(ErrVerbNoTranscript, "window "+echoName(p.Window)+" has no transcript", &VerbHint{
			Detail: "The pane is not joined to an agent's transcript. The join is made when the agent's hooks report its transcript, or when the daemon finds the one file that belongs to the pane.",
		})
	}
	if reg == nil {
		return nil, unsupportedHarnessError(harnessID)
	}
	if tr := reg.TranscriptFor(harnessID); tr == nil || tr.Reader != harness.ReaderJSONL {
		return nil, unsupportedHarnessError(harnessID)
	}
	page, err := transcriptview.Read(path, transcriptview.Options{
		After:  p.After,
		Before: p.Before,
		Limit:  p.Limit,
		Clean:  transcriptText,
	})
	if err != nil {
		if errors.Is(err, transcriptview.ErrNoFile) {
			return nil, newVerbError(ErrVerbNoTranscript, "the transcript of window "+echoName(p.Window)+" is gone")
		}
		// The error would name the file, which is the person's project and
		// session, so it is not passed on.
		return nil, newVerbError(ErrVerbInternal, "could not read the transcript")
	}
	entries := page.Entries
	if entries == nil {
		entries = []transcriptview.Entry{}
	}
	out := map[string]any{
		"type":      "agent_transcript",
		"session":   sess.Name(),
		"window":    w.ID,
		"harness":   harnessID,
		"cursor":    page.Cursor,
		"reset":     page.Reset,
		"more":      page.More,
		"older":     page.Older,
		"entries":   entries,
		"untrusted": true,
	}
	return out, nil
}

// unsupportedHarnessError refuses a pane whose harness has no transcript this
// daemon can read.
func unsupportedHarnessError(harnessID string) *verbError {
	return hintedVerbError(ErrVerbUnsupportedHarness, "the transcript of "+echoName(harnessID)+" cannot be read", &VerbHint{
		Detail: "agent-transcript reads Claude Code transcripts. Read the pane with capture-pane or stream-pane instead.",
	})
}

// transcriptJoinOf returns the file a window is joined to and its harness.
func (s *Session) transcriptJoinOf(windowID string) (path, harnessID string, ok bool) {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	j, ok := s.transcripts.joins[windowID]
	if !ok {
		return "", "", false
	}
	return j.reader.Path(), j.harness, true
}

// noteTranscriptGrowth raises a transcript event when the joined file grew
// since the last one. The event carries the cursor a read to the end would
// return now, and nothing of the file.
func (s *Session) noteTranscriptGrowth(windowID string, j *transcriptJoin) {
	off := j.reader.Offset()
	s.transcripts.mu.Lock()
	grew := off > 0 && off != j.announced
	if grew {
		j.announced = off
	}
	s.transcripts.mu.Unlock()
	if !grew {
		return
	}
	cursor, err := transcriptview.CursorAt(j.reader.Path(), off)
	if err != nil {
		return
	}
	s.emit(SessionEvent{Type: EventTranscript, Window: windowID, Cursor: cursor})
}

// transcriptText makes the agent's text safe to show and keeps its lines:
// escape sequences and control characters other than newline and tab are removed, and so are the
// bidirectional controls that make text read in another order than it runs.
// Likely secrets are masked as attentionText masks them.
func transcriptText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	// A command's output keeps its colours in the transcript. The escape
	// sequences go whole, so no "[31m" is left where the escape was.
	if strings.ContainsRune(s, 0x1b) || strings.ContainsRune(s, 0x9b) {
		s = ansi.Strip(s)
	}
	clean := strings.IndexFunc(s, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\n' && r != '\t') || isBidiControl(r)
	}) < 0
	if !clean {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			if (unicode.IsControl(r) && r != '\n' && r != '\t') || isBidiControl(r) {
				continue
			}
			b.WriteRune(r)
		}
		s = b.String()
	}
	if attentionMaySecret(s) {
		s = attentionSecret().ReplaceAllString(s, "${1}[redacted]")
	}
	return s
}

// isBidiControl reports the embedding, override and isolate controls.
func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}
