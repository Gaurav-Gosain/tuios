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

// The nesting probe.
//
// The process tests in nested_attach.go place a client that runs in a pane,
// or under one. They cannot place a client whose output reaches a pane some
// other way: through ssh to this machine, script run with setsid, or any
// relay that gives it a terminal of its own and drops the pane's variables.
// Such a client still shows the session inside itself. Its size shrinks the
// session to the floor, and it redraws its own pane without end.
//
// What such a client cannot hide is its output, because that output is what
// makes the loop. So a client writes a probe to its terminal before it
// attaches: an OSC sequence no terminal acts on, carrying a random nonce, and
// sends the same nonce with the attach. Every pane's output is scanned for
// probes. When the daemon has seen the client's nonce in a pane, it knows the
// session that pane belongs to, and it places the client there.
//
// A relay that rewrites the screen instead of passing bytes (mosh, tmux) does
// not carry the probe, and a client behind one is not placed.

// nestProbePrefix starts a probe. 7717 is not an OSC number any terminal
// assigns a meaning to, so a terminal that is not a pane ignores it.
const (
	nestProbePrefix = "\x1b]7717;tuios-nest;"
	nestProbeEnd    = "\x1b\\"
	nestNonceLen    = 32
	nestProbeMax    = len(nestProbePrefix) + nestNonceLen + len(nestProbeEnd)
)

// nestProbeWatch is how long after an attach the daemon keeps watching pane
// output for the client's probe. The attach does not wait for it. A relay
// passes the probe on in milliseconds, and ssh over a slow link in the time
// of one trip, so two seconds covers both with room to spare.
const nestProbeWatch = 2 * time.Second

// nestSightingTTL is how long a sighting is kept for an attach to claim it.
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

// nestSightings records the pane session each probe was seen in, and the
// watches waiting for a probe that was not seen by the time its client
// attached. It is global because a PTY does not hold its daemon, and a nonce
// is random, so two daemons in one process (tests) cannot confuse each other's.
var nestSightings = struct {
	sync.Mutex
	seen    map[string]nestSighting
	watches map[string]func(sessionID string)
}{seen: make(map[string]nestSighting), watches: make(map[string]func(string))}

type nestSighting struct {
	sessionID string
	at        time.Time
}

// scanNestProbes records every probe in data against this pane's session.
func (p *PTY) scanNestProbes(data []byte) {
	if p.sessionID == "" {
		return
	}
	if len(p.probeTail) > 0 {
		// A probe split across the last read and this one.
		joined := append(append([]byte(nil), p.probeTail...), data[:min(len(data), nestProbeMax)]...)
		recordNestProbes(joined, p.sessionID)
	}
	recordNestProbes(data, p.sessionID)
	keep := min(len(data), nestProbeMax-1)
	p.probeTail = append(p.probeTail[:0], data[len(data)-keep:]...)
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
			// Off the pane's read loop: the watch detaches a client, which
			// takes locks the read loop must not wait on.
			go watch(sessionID)
		}
	}
}

// seenNestProbe returns the session ID of the pane the probe nonce was seen
// in, or "" when it has not been seen.
func seenNestProbe(nonce string) string {
	if nonce == "" {
		return ""
	}
	nestSightings.Lock()
	defer nestSightings.Unlock()
	return nestSightings.seen[nonce].sessionID
}

// watchNestProbe calls fn with the pane's session ID if the probe nonce shows
// up within d. A probe can arrive after its client attached, over a slow ssh
// link, so the attach is not held up waiting for it: the watch catches it
// afterwards. A probe seen between the attach's check and this call is
// answered at once.
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
