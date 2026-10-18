package tml

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits bound what one document may hold. Over a limit is a problem with
// the code limit, and the limit named in its message.
type Limits struct {
	DocBytes   int // the source text or JSON
	Nodes      int // components and text runs, before each expands
	Depth      int // nesting of nodes
	StateBytes int // the state store as compact JSON
	Each       int // nodes that each, list and table rows expand to, in all
	TableRows  int // rows of one table
	PatchBytes int // one JSON Patch
	Images     int // image nodes in a document
	ImageBytes int // one decoded data: image
	Problems   int // problems reported in one pass
}

// DefaultLimits are the limits for a document from a program, a plugin or an
// agent.
func DefaultLimits() Limits {
	return Limits{
		DocBytes:   256 << 10,
		Nodes:      5000,
		Depth:      32,
		StateBytes: 1 << 20,
		Each:       2000,
		TableRows:  50000,
		PatchBytes: 64 << 10,
		Images:     4,
		ImageBytes: 4 << 20,
		Problems:   20,
	}
}

// PersonLimits are the limits for a document the person opens from their own
// command line: a larger document, the rest the same.
func PersonLimits() Limits {
	l := DefaultLimits()
	l.DocBytes = 1 << 20
	return l
}

// Rates a host applies per surface or per pane. The library gives the
// numbers and a Limiter. It holds no surfaces itself.
const (
	PatchesPerSecond = 100             // patches per surface
	ToastInterval    = 2 * time.Second // one toast per pane per interval
	SurfacesPerPane  = 8               // open surfaces per pane
	ExprSteps        = 10000           // expression steps per surface per frame
)

// Limiter is a token bucket: it allows Rate events a second, with bursts up
// to Burst. It is safe for use by several goroutines.
type Limiter struct {
	Rate  float64
	Burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
	primed bool
}

// NewLimiter returns a Limiter. NewLimiter(PatchesPerSecond, PatchesPerSecond)
// is the patch limit of one surface. NewLimiter(1/ToastInterval.Seconds(), 1)
// is the toast limit of one pane.
func NewLimiter(rate, burst float64) *Limiter {
	return &Limiter{Rate: rate, Burst: burst}
}

// Allow reports whether one event may happen at now, and takes a token if so.
func (l *Limiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.primed {
		l.tokens, l.last, l.primed = l.Burst, now, true
	}
	if dt := now.Sub(l.last).Seconds(); dt > 0 {
		l.tokens = min(l.Burst, l.tokens+dt*l.Rate)
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// Sanitize removes what must never reach a terminal from text a document
// carries: C0 and C1 control characters other than tab and newline, DEL, and
// the bidirectional embedding, override and isolate characters. Invalid UTF-8
// becomes U+FFFD. The parsers apply it to every text run and attribute, so a
// tree never holds an escape sequence.
func Sanitize(s string) string {
	clean := true
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || unsafeRune(r) {
			clean = false
			break
		}
		i += size
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case unsafeRune(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}
