package session

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
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

// nestProbeWindow is how long after the probe was written the daemon waits for
// it to show up in a pane before it concludes the client is not in one. A
// relay passes it on in a few milliseconds. The client writes it before it
// detects the terminal and dials, so most of the window has already passed
// when the attach arrives.
const nestProbeWindow = 200 * time.Millisecond

// nestSightingTTL is how long a sighting is kept for an attach to claim it.
const nestSightingTTL = 30 * time.Second

// NewNestProbe returns a fresh nonce and the probe sequence that carries it.
func NewNestProbe() (nonce string, seq []byte) {
	var b [nestNonceLen / 2]byte
	_, _ = rand.Read(b[:])
	nonce = hex.EncodeToString(b[:])
	return nonce, []byte(nestProbePrefix + nonce + nestProbeEnd)
}

// NestProbe is a probe a client wrote to its terminal.
type NestProbe struct {
	nonce string
	at    time.Time
}

// WriteNestProbe writes a fresh probe to w, which must be the terminal the
// client renders to. It is written as early as the client can, so the probe
// has reached any pane it is going to reach by the time the client attaches.
func WriteNestProbe(w io.Writer) NestProbe {
	nonce, seq := NewNestProbe()
	if _, err := w.Write(seq); err != nil {
		return NestProbe{}
	}
	return NestProbe{nonce: nonce, at: time.Now()}
}

// SetNestProbe records the probe on the client, so every attach sends it. Call
// it before the first attach.
func (c *TUIClient) SetNestProbe(p NestProbe) {
	c.nestProbe = p.nonce
	c.nestProbeAt = p.at
}

// nestProbeFields are the probe fields for an attach payload.
func (c *TUIClient) nestProbeFields() (string, int) {
	if c.nestProbe == "" {
		return "", 0
	}
	return c.nestProbe, int(time.Since(c.nestProbeAt) / time.Millisecond)
}

// nestSightings records the pane sessions each probe was seen in. It is global
// because a PTY does not hold its daemon, and a nonce is random, so two
// daemons in one process (tests) cannot confuse each other's.
var nestSightings = struct {
	sync.Mutex
	seen map[string]nestSighting
	cond *sync.Cond
}{seen: make(map[string]nestSighting)}

func init() { nestSightings.cond = sync.NewCond(&nestSightings.Mutex) }

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
		now := time.Now()
		nestSightings.Lock()
		for k, s := range nestSightings.seen {
			if now.Sub(s.at) > nestSightingTTL {
				delete(nestSightings.seen, k)
			}
		}
		nestSightings.seen[nonce] = nestSighting{sessionID: sessionID, at: now}
		nestSightings.cond.Broadcast()
		nestSightings.Unlock()
		b = b[nestNonceLen:]
	}
}

// awaitNestProbe returns the session ID of the pane the probe nonce was seen
// in, or "" when it was not seen. ageMs is how long ago the client wrote it.
// It waits until the probe is nestProbeWindow old.
func awaitNestProbe(nonce string, ageMs int) string {
	if nonce == "" {
		return ""
	}
	deadline := time.Now().Add(nestProbeWindow - time.Duration(ageMs)*time.Millisecond)
	// Wake the wait at the deadline even if no probe arrives.
	timer := time.AfterFunc(time.Until(deadline), func() {
		nestSightings.Lock()
		nestSightings.cond.Broadcast()
		nestSightings.Unlock()
	})
	defer timer.Stop()
	nestSightings.Lock()
	defer nestSightings.Unlock()
	for {
		if s, ok := nestSightings.seen[nonce]; ok {
			return s.sessionID
		}
		if !time.Now().Before(deadline) {
			return ""
		}
		nestSightings.cond.Wait()
	}
}
