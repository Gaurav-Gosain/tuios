package session

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

// This file carries the machine-readable remedy surface of the verb protocol.
//
// Every error envelope may carry a "hint" object alongside its stable code and
// human message. The hint names the thing that resolves the failure: the verb to
// call, the CLI command to run, the parameter that was wrong and what it
// accepts, the closest spelling to what the caller asked for, and the set of
// values that do exist. All fields are omitempty, so an existing consumer that
// only reads code and message is unaffected.

// The closed value sets the protocol accepts. They are the single source of
// truth for both the list-verbs parameter schema and the "accepted" field on an
// invalid_params hint, so the two can never drift apart.
var (
	// captureSources are the buffers capture-pane can read.
	captureSources = []string{"visible", "recent"}
	// retiredCaptureSources maps a capture source that was once accepted to the
	// reason it no longer is, so the rejection can say what happened rather than
	// only listing what is allowed. "recent-unwrapped" was documented as reserved
	// and behaved as a silent alias for "recent"; the emulator does not record
	// which rows are soft wrapped (the scrollback wrap flag is written as a
	// constant and the live screen carries none), so unwrapping cannot be
	// implemented without guessing at row boundaries.
	retiredCaptureSources = map[string]string{
		"recent-unwrapped": "unwrapped capture is not implemented; it previously returned the same physical rows as \"recent\" without unwrapping them",
	}
	// waitConditions are the conditions wait-for understands.
	waitConditions = []string{"session-exists", "window-output", "window-exit", "window-idle"}
	// knownEventTypes are the event types a subscribe filter can name.
	knownEventTypes = []string{
		EventWindowCreated, EventWindowClosed, EventWindowExit, EventWindowRetitled,
		EventWindowFocused, EventWindowMoved, EventWindowMinimized, EventWindowRestored,
		EventWorkspaceSwitched, EventOutput, EventBell, EventModeChanged,
		EventSessionCreated, EventSessionClosed, EventGap,
	}
)

// errorCodeCatalog documents every stable error code for the list-verbs result,
// so an agent can learn the failure vocabulary without reading the docs. Keep it
// in sync with the ErrVerb* constants.
var errorCodeCatalog = []struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}{
	{ErrVerbInvalidRequest, "The line was not a valid request envelope, or the connection is in the wrong state for the verb."},
	{ErrVerbUnknownVerb, "No such verb. The hint carries the closest match and the full verb list."},
	{ErrVerbInvalidParams, "A parameter was missing, malformed, or outside its accepted set. The hint names the parameter."},
	{ErrVerbSessionNotFound, "The named session does not exist. The hint lists the sessions that do."},
	{ErrVerbWindowNotFound, "The window target did not resolve. The hint lists the addressable windows."},
	{ErrVerbNoWindows, "The session exists but holds no windows to act on."},
	{ErrVerbPTYNotFound, "The target window has no live PTY; its shell has already exited."},
	{ErrVerbNeedsClient, "The verb needs an attached client to render it, and none is attached."},
	{ErrVerbOptionNotFound, "The option was never set on this session."},
	{ErrVerbCommandFailed, "The verb was routed to the attached client and came back failed."},
	{ErrVerbTimeout, "A wait-for condition did not match before its timeout elapsed."},
	{ErrVerbProtocolMismatch, "The caller's protocol version is outside the range this daemon accepts."},
	{ErrVerbInternal, "Unexpected server-side failure."},
}

// VerbHint is the structured remedy attached to an error envelope. Every field
// is optional; a hint is only attached when at least one field is meaningful.
type VerbHint struct {
	// Verb names the protocol verb that resolves or explains the failure, e.g.
	// "list-verbs" for an unknown verb or "new-window" for an empty session.
	Verb string `json:"verb,omitempty"`
	// Command is the exact CLI invocation that resolves the failure, written so
	// it can be copied and run as-is (placeholders are in <angle brackets>).
	Command string `json:"command,omitempty"`
	// Param names the offending parameter for invalid_params.
	Param string `json:"param,omitempty"`
	// Accepted lists the values Param will take, when that set is closed.
	Accepted []string `json:"accepted,omitempty"`
	// DidYouMean is the closest match to what the caller asked for, when one is
	// close enough to be worth suggesting.
	DidYouMean string `json:"did_you_mean,omitempty"`
	// Available lists what does exist (session names, window ids, verb names),
	// so an agent can pick a valid target without a second round trip.
	Available []string `json:"available,omitempty"`
	// Detail is one sentence of extra context that does not fit the fields
	// above, such as why a wait-for timed out.
	Detail string `json:"detail,omitempty"`
}

// empty reports whether the hint carries nothing worth serializing.
func (h *VerbHint) empty() bool {
	return h == nil || (h.Verb == "" && h.Command == "" && h.Param == "" &&
		len(h.Accepted) == 0 && h.DidYouMean == "" && len(h.Available) == 0 && h.Detail == "")
}

// hintedVerbError builds a *verbError carrying a hint. A hint with no populated
// field is dropped so the envelope stays clean.
func hintedVerbError(code, message string, hint *VerbHint) *verbError {
	e := newVerbError(code, message)
	if !hint.empty() {
		e.Hint = hint
	}
	return e
}

// invalidParam builds an invalid_params error naming the offending parameter and
// the values it accepts (accepted may be nil when the set is open).
func invalidParam(param, message string, accepted ...string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, message, &VerbHint{
		Param:    param,
		Accepted: accepted,
		Verb:     "list-verbs",
	})
}

// validateCaptureSource checks a capture-pane source against the accepted set.
// An empty source is valid and means the default. A source that was once
// accepted is rejected with the reason it went away, so a caller that was
// relying on it learns why rather than only what to use instead.
func validateCaptureSource(source string) *verbError {
	if source == "" || slices.Contains(captureSources, source) {
		return nil
	}
	msg := "unknown capture source " + strconv.Quote(source)
	hint := &VerbHint{
		Param:    "source",
		Accepted: captureSources,
		Verb:     "list-verbs",
	}
	if reason, retired := retiredCaptureSources[source]; retired {
		// A retired value is a closed-set miss with history: name the successor
		// directly instead of relying on edit distance, which would not connect
		// "recent-unwrapped" to "recent".
		hint.DidYouMean = "recent"
		hint.Detail = reason
	} else {
		hint.DidYouMean = closestMatch(source, captureSources)
	}
	return hintedVerbError(ErrVerbInvalidParams, msg, hint)
}

// closestMatch returns the candidate closest to target by edit distance, or ""
// when nothing is close enough to suggest. The threshold scales with the length
// of the target so short names do not match everything: a 3-character target
// tolerates one edit, a 10-character target tolerates three.
func closestMatch(target string, candidates []string) string {
	if target == "" || len(candidates) == 0 {
		return ""
	}
	// Scale the tolerance with the length of the target so a short name does not
	// match everything, and cap it so two long but genuinely different names
	// ("list-windows" and "close-window") are never suggested for each other.
	limit := min(len(target)/4+1, 3)

	best := ""
	bestDist := limit + 1
	for _, c := range candidates {
		if c == target {
			// An exact match is never a suggestion; the caller failed for some
			// other reason and "did you mean the thing you typed" is noise.
			continue
		}
		d := editDistance(strings.ToLower(target), strings.ToLower(c))
		if d < bestDist || (d == bestDist && c < best) {
			bestDist = d
			best = c
		}
	}
	if bestDist > limit {
		return ""
	}
	return best
}

// editDistance returns the Levenshtein distance between a and b using a single
// rolling row, which is enough for the short identifiers compared here.
func editDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// knownVerbNames returns every registered verb name in sorted order.
func knownVerbNames() []string {
	names := make([]string, 0, len(verbRegistry))
	for name := range verbRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sessionNames returns the names of every live session, sorted, for the
// available list on a session_not_found hint.
func (d *Daemon) sessionNames() []string {
	infos := d.manager.ListSessions()
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names
}

// windowTargets returns the addressable identifiers of a session's windows (id
// and display name for each), sorted, for a window_not_found hint. Ids and names
// are both accepted by the window parameter, so both belong in the list.
func windowTargets(state *SessionState) []string {
	if state == nil {
		return nil
	}
	targets := make([]string, 0, len(state.Windows)*2)
	for _, w := range state.Windows {
		if w.ID != "" {
			targets = append(targets, w.ID)
		}
		name := w.CustomName
		if name == "" {
			name = w.Title
		}
		if name != "" && name != w.ID {
			targets = append(targets, name)
		}
	}
	sort.Strings(targets)
	return targets
}
