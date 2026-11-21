package session

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
	"github.com/charmbracelet/x/ansi"
)

func (d *Daemon) verbAgentTranscript(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		HumanNonce string `json:"human_nonce"`
		After      string `json:"after"`
		Before     string `json:"before"`
		Limit      *int   `json:"limit"`
		Image      string `json:"image"`
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
	limit := 0
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > transcriptview.MaxLimit {
			return nil, invalidParam("limit", "limit must be between 1 and "+strconv.Itoa(transcriptview.MaxLimit))
		}
		limit = *p.Limit
	}
	if !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "agent-transcript is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was read. Only a client attached now, or a connection that holds a presence, can read a transcript, by passing its nonce. An agent never can.",
		})
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if _, ok := d.humanNonceFor(p.HumanNonce, sess.ID, cs); !ok {
		return nil, nonceScopeError("agent-transcript")
	}
	st := sess.GetState()
	idx, err := findWindowStateIndex(st.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := st.Windows[idx]
	reg := d.agentMatcher.registry
	path, harnessID, format, session, leaf, joined := sess.transcriptViewOf(w.ID)
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
	if tr := reg.TranscriptFor(harnessID); tr == nil || tr.Reader != format || !transcriptview.Known(format) {
		return nil, unsupportedHarnessError(harnessID)
	}
	opts := transcriptview.Options{
		After:      p.After,
		Before:     p.Before,
		Limit:      limit,
		Clean:      transcriptText,
		CleanLines: maskSecretLines,
		Format:     format,
		Session:    session,
		Leaf:       leaf,
	}
	if p.Image != "" {
		return d.transcriptImage(sess, w.ID, path, format, p.Image, opts)
	}
	page, err := transcriptview.Read(path, opts)
	if err != nil {
		if errors.Is(err, transcriptview.ErrNoFile) {
			return nil, newVerbError(ErrVerbNoTranscript, "the transcript of window "+echoName(p.Window)+" is gone")
		}
		if errors.Is(err, transcript.ErrNotRegular) {
			return nil, newVerbError(ErrVerbNoTranscript, "the transcript of window "+echoName(p.Window)+" is not a regular file, so it was not read")
		}
		if errors.Is(err, transcriptview.ErrService) {
			return nil, newVerbError(ErrVerbNoTranscript, "opencode did not give the session of window "+echoName(p.Window))
		}
		if errors.Is(err, transcriptview.ErrTooLarge) {
			return nil, newVerbError(ErrVerbNoTranscript, "the transcript of window "+echoName(p.Window)+" is too large to read")
		}
		if errors.Is(err, transcriptview.ErrPiTree) {
			return nil, newVerbError(ErrVerbInternal, "the Pi session tree is invalid or unavailable")
		}
		return nil, newVerbError(ErrVerbInternal, "could not read the transcript")
	}
	entries := withFileImages(page.Entries, d.pasteDir())
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

func (d *Daemon) transcriptImage(sess *Session, window, path, format, id string, opts transcriptview.Options) (any, *verbError) {
	var mediaType string
	var data []byte
	var err error
	if strings.HasPrefix(id, fileImagePrefix) {
		opts.After, opts.Before, opts.Limit = "", "", transcriptview.MaxLimit
		var page transcriptview.Page
		if page, err = transcriptview.Read(path, opts); err == nil {
			mediaType, data, err = readFileImage(page.Entries, d.pasteDir(), id)
		}
	} else {
		mediaType, data, err = transcriptview.ReadImage(path, format, id)
	}
	switch {
	case errors.Is(err, transcriptview.ErrNoFile):
		return nil, newVerbError(ErrVerbNoTranscript, "the transcript of window "+echoName(window)+" is gone")
	case errors.Is(err, transcriptview.ErrNoImage):
		return nil, invalidParam("image", "no image has the id "+echoName(id))
	case err != nil:
		return nil, newVerbError(ErrVerbInternal, "could not read the transcript")
	}
	return map[string]any{
		"type":       "agent_transcript_image",
		"session":    sess.Name(),
		"window":     window,
		"image":      id,
		"media_type": mediaType,
		"data":       data,
		"untrusted":  true,
	}, nil
}

func (d *Daemon) pasteDir() string {
	if d.pastes == nil {
		return ""
	}
	dir, err := d.pastes.dir()
	if err != nil {
		return ""
	}
	return dir
}

func unsupportedHarnessError(harnessID string) *verbError {
	return hintedVerbError(ErrVerbUnsupportedHarness, "the transcript of "+echoName(harnessID)+" cannot be read", &VerbHint{
		Detail: "agent-transcript reads the transcripts of Claude Code, Codex, Gemini CLI, Qwen Code, Copilot CLI and Amp. Read the pane with capture-pane or stream-pane instead.",
	})
}

func (s *Session) transcriptJoinOf(windowID string) (path, harnessID, format, session string, ok bool) {
	path, harnessID, format, session, _, ok = s.transcriptViewOf(windowID)
	return
}

func (s *Session) transcriptViewOf(windowID string) (path, harnessID, format, session string, leaf *string, ok bool) {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	j, ok := s.transcripts.joins[windowID]
	if !ok {
		return "", "", "", "", nil, false
	}
	if j.leaf != nil {
		value := *j.leaf
		leaf = &value
	}
	return j.reader.Path(), j.harness, j.format, j.session, leaf, true
}

func (s *Session) noteTranscriptGrowth(windowID string, j *transcriptJoin) {
	s.announceTranscript(windowID, j, j.reader.Offset())
}

func (s *Session) announceTranscript(windowID string, j *transcriptJoin, off int64) {
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

func transcriptText(s string) string {
	s = strings.ToValidUTF8(s, "�")
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
	if strings.IndexByte(s, '\n') >= 0 {
		lines := strings.Split(s, "\n")
		if maskSecretLines(lines) {
			s = strings.Join(lines, "\n")
		}
	}
	return s
}

func maskSecretLines(lines []string) bool {
	masked := false
	inKey := false
	for i, l := range lines {
		switch {
		case !inKey && strings.Contains(l, "-----BEGIN") && strings.Contains(l, "PRIVATE KEY"):
			inKey = true
		case inKey && strings.Contains(l, "-----END"):
			inKey = false
		case inKey:
			if l != secretMask {
				lines[i] = secretMask
				masked = true
			}
		}
	}
	masked = maskRuns(lines, isBase64Line, func(string) string { return secretMask }) || masked
	masked = maskRuns(lines, isEnvLine, func(l string) string {
		eq := strings.IndexByte(l, '=')
		return l[:eq+1] + secretMask
	}) || masked
	return masked
}

const secretMask = "[redacted]"

func maskRuns(lines []string, match func(string) bool, mask func(string) string) bool {
	masked := false
	for i := 0; i < len(lines); {
		j := i
		for j < len(lines) && match(lines[j]) {
			j++
		}
		if j-i >= 2 {
			for k := i; k < j; k++ {
				lines[k] = mask(lines[k])
			}
			masked = true
		}
		i = max(j, i+1)
	}
	return masked
}

func isBase64Line(l string) bool {
	l = strings.TrimSpace(l)
	if len(l) < 40 {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}

func isEnvLine(l string) bool {
	l = strings.TrimSpace(l)
	l = strings.TrimPrefix(l, "export ")
	eq := strings.IndexByte(l, '=')
	if eq < 1 || eq == len(l)-1 {
		return false
	}
	if l[eq+1:] == secretMask {
		return true
	}
	for i := range eq {
		c := l[i]
		if !(c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}
