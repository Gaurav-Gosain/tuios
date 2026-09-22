package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Agent metadata is a short list of key and value pairs a pane reports about
// the agent in it: the model, how full its context is, what the turn cost, a
// one-line summary of the task. The rail draws them under the agent's row.
//
// It is display only. Nothing reads it to decide a state, a wait, an alert or
// a message: a harness can put anything here, and a value that could steer
// tuios would be an input nobody validated. That is also why it has hard
// limits and a TTL: a statusline feed that stops writing leaves nothing stale
// behind for longer than it said.

// Limits on agent metadata. They bound what one pane can make every attached
// client store and draw.
const (
	// AgentMetaMaxPerCall is how many keys one set-agent-meta call may touch.
	AgentMetaMaxPerCall = 16
	// AgentMetaMaxPerPane is how many keys one pane may hold at once.
	AgentMetaMaxPerPane = 32
	// AgentMetaMaxKey is the longest key, in bytes. Keys are ASCII.
	AgentMetaMaxKey = 24
	// AgentMetaMaxValue is the longest value, in characters. A longer value is
	// cut, not refused, because a harness writing a summary cannot know how
	// long this build lets one be.
	AgentMetaMaxValue = 80
	// AgentMetaMaxTTL is the longest TTL a call may ask for.
	AgentMetaMaxTTL = 24 * time.Hour
)

// AgentMetaToken is one key and value a pane reported about its agent.
//
// It rides WindowState, so an older peer that does not know the field drops it
// on decode, and a state with no metadata carries nil, which every reader
// takes as "the pane said nothing".
type AgentMetaToken struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Source names who wrote it ("claude-statusline", "hook"), so a writer can
	// clear its own keys without touching another's. Empty when unstated.
	Source string `json:"source,omitempty"`
	// Expires is when the daemon drops it, as Unix nanoseconds, or 0 for a
	// token that lives until it is cleared or the agent leaves the pane.
	Expires int64 `json:"expires,omitempty"`
}

// AgentMetaUpdate is one set-agent-meta call, already validated.
type AgentMetaUpdate struct {
	// Keys and Values are the tokens to set, in the order the caller wrote
	// them. A nil value removes the key.
	Keys   []string
	Values []*string
	// Source is recorded on every token set, and scopes Clear.
	Source string
	// TTL is how long the tokens set live. Zero means until cleared.
	TTL time.Duration
	// Clear removes every token the source wrote, or every token when Source
	// is empty, before the new ones are applied.
	Clear bool
}

// errAgentMetaFull is the per-pane limit, reported to the caller.
var errAgentMetaFull = fmt.Errorf("a pane holds at most %d metadata keys", AgentMetaMaxPerPane)

// errNoAgentMetaChange tells mutateState a prune found nothing to drop, so it
// neither bumps the version nor pushes to clients.
var errNoAgentMetaChange = errors.New("no agent metadata change")

// ValidAgentMetaKey reports whether k can be a metadata key: 1 to 24 bytes of
// lower-case letters, digits, '_' and '-', starting with a letter. Keys end up
// in config (the rail's $name tokens), so they are held to what a config key
// can spell.
func ValidAgentMetaKey(k string) bool {
	if k == "" || len(k) > AgentMetaMaxKey {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// CleanAgentMetaValue makes a value safe to draw: control characters (which
// include the escape that starts a terminal sequence) become spaces, runs of
// space fold to one, the ends are trimmed, and the result is cut to
// AgentMetaMaxValue characters. It reports whether it cut anything.
func CleanAgentMetaValue(v string) (string, bool) {
	var b strings.Builder
	b.Grow(len(v))
	space := false
	n := 0
	cut := false
	for _, r := range v {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			r = ' '
		}
		if unicode.IsSpace(r) {
			if space || b.Len() == 0 {
				continue
			}
			space = true
			r = ' '
		} else {
			space = false
		}
		if n == AgentMetaMaxValue {
			cut = true
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimRight(b.String(), " "), cut
}

// liveAgentMeta returns the tokens of cur that have not expired at now. It
// returns cur itself when nothing has, so the common case copies nothing.
func liveAgentMeta(cur []AgentMetaToken, now int64) []AgentMetaToken {
	expired := 0
	for _, t := range cur {
		if t.Expires != 0 && t.Expires <= now {
			expired++
		}
	}
	if expired == 0 {
		return cur
	}
	if expired == len(cur) {
		return nil
	}
	out := make([]AgentMetaToken, 0, len(cur)-expired)
	for _, t := range cur {
		if t.Expires == 0 || t.Expires > now {
			out = append(out, t)
		}
	}
	return out
}

// applyAgentMeta returns cur with u applied at now. It never writes into cur:
// state snapshots share a window's slice, so a token list is replaced whole
// and never edited in place. A key already present keeps its position, and a
// new key goes at the end, so the rail's order is the order keys first
// arrived in and does not reshuffle on every update.
func applyAgentMeta(cur []AgentMetaToken, u AgentMetaUpdate, now int64) ([]AgentMetaToken, error) {
	next := make([]AgentMetaToken, 0, len(cur)+len(u.Keys))
	for _, t := range liveAgentMeta(cur, now) {
		if u.Clear && (u.Source == "" || t.Source == u.Source) {
			continue
		}
		next = append(next, t)
	}
	var expires int64
	if u.TTL > 0 {
		expires = now + int64(u.TTL)
	}
	for i, key := range u.Keys {
		at := -1
		for j := range next {
			if next[j].Key == key {
				at = j
				break
			}
		}
		v := u.Values[i]
		if v == nil {
			if at >= 0 {
				next = append(next[:at], next[at+1:]...)
			}
			continue
		}
		tok := AgentMetaToken{Key: key, Value: *v, Source: u.Source, Expires: expires}
		if at >= 0 {
			next[at] = tok
		} else {
			next = append(next, tok)
		}
	}
	if len(next) > AgentMetaMaxPerPane {
		return nil, errAgentMetaFull
	}
	if len(next) == 0 {
		return nil, nil
	}
	return next, nil
}

// earliestAgentMetaExpiry is the soonest a token in the session expires, or 0
// when none will.
func earliestAgentMetaExpiry(windows []WindowState) int64 {
	var at int64
	for i := range windows {
		for _, t := range windows[i].AgentMeta {
			if t.Expires != 0 && (at == 0 || t.Expires < at) {
				at = t.Expires
			}
		}
	}
	return at
}

// SetAgentMeta applies u to the window target resolves to and returns the
// tokens the window holds afterwards.
func (s *Session) SetAgentMeta(target string, u AgentMetaUpdate) ([]AgentMetaToken, error) {
	var out []AgentMetaToken
	var next int64
	err := s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		tokens, err := applyAgentMeta(w.AgentMeta, u, time.Now().UnixNano())
		if err != nil {
			return err
		}
		w.AgentMeta = tokens
		out = tokens
		next = earliestAgentMetaExpiry(st.Windows)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.armAgentMetaPrune(next)
	return out, nil
}

// armAgentMetaPrune makes sure a prune runs by at. A timer already due sooner
// stands. The prune is what makes a TTL mean something to a client: clients
// only draw what the state sync hands them, so the daemon drops the token and
// pushes, and no client has to keep a clock of its own for it.
func (s *Session) armAgentMetaPrune(at int64) {
	if at == 0 {
		return
	}
	s.agentMetaMu.Lock()
	defer s.agentMetaMu.Unlock()
	if s.agentMetaTimer != nil && s.agentMetaAt != 0 && s.agentMetaAt <= at {
		return
	}
	if s.agentMetaTimer != nil {
		s.agentMetaTimer.Stop()
	}
	s.agentMetaAt = at
	s.agentMetaTimer = time.AfterFunc(max(time.Until(time.Unix(0, at)), 0), s.pruneAgentMeta)
}

// pruneAgentMeta drops every expired token in the session and arms the next
// prune.
func (s *Session) pruneAgentMeta() {
	s.agentMetaMu.Lock()
	s.agentMetaTimer, s.agentMetaAt = nil, 0
	s.agentMetaMu.Unlock()

	var next int64
	_ = s.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		changed := false
		for i := range st.Windows {
			w := &st.Windows[i]
			if live := liveAgentMeta(w.AgentMeta, now); len(live) != len(w.AgentMeta) {
				w.AgentMeta = live
				changed = true
			}
		}
		next = earliestAgentMetaExpiry(st.Windows)
		if !changed {
			return errNoAgentMetaChange
		}
		return nil
	})
	s.armAgentMetaPrune(next)
}

// stopAgentMetaTimer cancels a pending prune, for a session that is stopping.
func (s *Session) stopAgentMetaTimer() {
	s.agentMetaMu.Lock()
	defer s.agentMetaMu.Unlock()
	if s.agentMetaTimer != nil {
		s.agentMetaTimer.Stop()
		s.agentMetaTimer, s.agentMetaAt = nil, 0
	}
}

// decodeAgentMetaTokens reads the tokens object of a set-agent-meta call in
// the order it was written, which a map would lose. Each value is a string or
// null. Keys repeated in one object keep the last value, in the first one's
// place.
func decodeAgentMetaTokens(raw json.RawMessage) ([]string, []*string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, errors.New("tokens must be an object of key to value")
	}
	var keys []string
	var values []*string
	seen := map[string]int{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, _ := kt.(string)
		var v *string
		if err := dec.Decode(&v); err != nil {
			return nil, nil, fmt.Errorf("tokens.%s must be a string or null", key)
		}
		if i, ok := seen[key]; ok {
			values[i] = v
			continue
		}
		seen[key] = len(keys)
		keys = append(keys, key)
		values = append(values, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	return keys, values, nil
}
