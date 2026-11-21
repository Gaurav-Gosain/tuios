package session

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	nestProbePrefix = "\x1b]7717;tuios-nest;"
	nestProbeEnd    = "\x1b\\"
	nestNonceLen    = 32
	nestProbeMax    = len(nestProbePrefix) + nestNonceLen + len(nestProbeEnd)
)

const nestProbeWatch = 2 * time.Second

const nestSightingTTL = 30 * time.Second

// NestProbeSafe reports whether a terminal with this TERM ignores the probe.
// Terminals that parse OSC drop an OSC number they do not know without
// drawing it: xterm, kitty, ghostty, alacritty, wezterm, iTerm2,
// Terminal.app, Windows Terminal, and tmux and screen as outer terminals. The
// Linux console does not parse OSC and would print the text, and a dumb or
// hardware VT terminal may do the same, so those get no probe.
func NestProbeSafe(term string) bool {
	switch {
	case term == "", term == "dumb", term == "linux", strings.HasPrefix(term, "linux-"),
		strings.HasPrefix(term, "vt"), strings.HasPrefix(term, "cons"):
		return false
	}
	return true
}

// NewNestProbe returns a fresh nonce and the probe sequence that carries it.
func NewNestProbe() (nonce string, seq []byte) {
	var b [nestNonceLen / 2]byte
	_, _ = rand.Read(b[:])
	nonce = hex.EncodeToString(b[:])
	return nonce, []byte(nestProbePrefix + nonce + nestProbeEnd)
}

// NestProbe is a probe a client wrote to its terminal.
type NestProbe struct{ nonce string }

// WriteNestProbe writes a fresh probe to w, which must be the terminal the
// client renders to. It is written as early as the client can, so the probe
// has reached any pane it is going to reach by the time the client attaches.
func WriteNestProbe(w io.Writer) NestProbe {
	nonce, seq := NewNestProbe()
	if _, err := w.Write(seq); err != nil {
		return NestProbe{}
	}
	return NestProbe{nonce: nonce}
}

// SetNestProbe records the probe on the client, so every attach sends it. Call
// it before the first attach.
func (c *TUIClient) SetNestProbe(p NestProbe) {
	c.nestProbe = p.nonce
}

var nestSightings = struct {
	sync.Mutex
	seen    map[string]nestSighting
	watches map[string]func(sessionID string)
}{seen: make(map[string]nestSighting), watches: make(map[string]func(string))}

type nestSighting struct {
	sessionID string
	at        time.Time
}

func (p *PTY) scanNestProbes(data []byte) {
	sessionID := p.ownerID()
	if sessionID == "" {
		return
	}
	if len(p.probeTail) > 0 {
		n := copy(p.probeJoin[:], p.probeTail)
		n += copy(p.probeJoin[n:], data[:min(len(data), nestProbeMax)])
		recordNestProbes(p.probeJoin[:n], sessionID)
	}
	recordNestProbes(data, sessionID)
	keep := data[len(data)-min(len(data), nestProbeMax-1):]
	if i := bytes.LastIndexByte(keep, 0x1b); i >= 0 {
		p.probeTail = append(p.probeTail[:0], keep[i:]...)
	} else {
		p.probeTail = p.probeTail[:0]
	}
}

func recordNestProbes(b []byte, sessionID string) {
	prefix := []byte(nestProbePrefix)
	for {
		i := bytes.Index(b, prefix)
		if i < 0 {
			return
		}
		b = b[i+len(prefix):]
		if len(b) < nestNonceLen {
			return
		}
		nonce := string(b[:nestNonceLen])
		b = b[nestNonceLen:]
		now := time.Now()
		nestSightings.Lock()
		for k, s := range nestSightings.seen {
			if now.Sub(s.at) > nestSightingTTL {
				delete(nestSightings.seen, k)
			}
		}
		nestSightings.seen[nonce] = nestSighting{sessionID: sessionID, at: now}
		watch := nestSightings.watches[nonce]
		delete(nestSightings.watches, nonce)
		nestSightings.Unlock()
		if watch != nil {
			go watch(sessionID)
		}
	}
}

func seenNestProbe(nonce string) string {
	if nonce == "" {
		return ""
	}
	nestSightings.Lock()
	defer nestSightings.Unlock()
	return nestSightings.seen[nonce].sessionID
}

func watchNestProbe(nonce string, d time.Duration, fn func(sessionID string)) {
	if nonce == "" {
		return
	}
	nestSightings.Lock()
	if s, ok := nestSightings.seen[nonce]; ok {
		nestSightings.Unlock()
		go fn(s.sessionID)
		return
	}
	nestSightings.watches[nonce] = fn
	nestSightings.Unlock()
	time.AfterFunc(d, func() {
		nestSightings.Lock()
		delete(nestSightings.watches, nonce)
		nestSightings.Unlock()
	})
}

// DetachedReason is the daemon's reason when it took this client off its
// session because another client attached with -d, single_client is on, or
// detach-client named it. It is "" otherwise. See detach_client.go.
func (c *TUIClient) DetachedReason() string {
	if r := c.detachedReason.Load(); r != nil {
		return *r
	}
	return ""
}

// NestedRefusal is the daemon's reason when it took this client off its
// session after the attach, because the client's output reached a pane of the
// session it shows. It is "" otherwise.
func (c *TUIClient) NestedRefusal() string {
	if r := c.nestedRefusal.Load(); r != nil {
		return *r
	}
	return ""
}
