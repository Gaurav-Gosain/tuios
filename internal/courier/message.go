package courier

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

// Limits on one message. They bound what a peer can make a reader hold, and
// what an agent gets handed in one read.
const (
	MaxBodyBytes    = 64 << 10
	MaxSubjectBytes = 200
	// MaxMessageAge is the oldest message a client accepts. It is longer than
	// the longest relay TTL (MaxRelayTTL), so the local store remembers every
	// message the relay could still hand over again, and a replay of one the
	// store has forgotten is refused for its age.
	MaxMessageAge = 8 * 24 * time.Hour
	// MaxRelayTTL is the longest a relay keeps a message.
	MaxRelayTTL = 7 * 24 * time.Hour
	// maxClockAhead is how far in the future a sender's clock may be.
	maxClockAhead = 10 * time.Minute
)

var (
	idPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	agentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)
)

// Message is what one person's agent writes to another's.
type Message struct {
	V int `json:"v"`
	// ID is random and chosen by the sender. Thread is the ID of the message
	// that started the conversation, and is ID itself for a new one.
	ID      string `json:"id"`
	Thread  string `json:"thread"`
	ReplyTo string `json:"reply_to,omitempty"`
	// Agent labels which of the recipient's agents the message is for. Empty
	// is any of them.
	Agent   string    `json:"agent,omitempty"`
	Subject string    `json:"subject,omitempty"`
	Body    string    `json:"body"`
	SentAt  time.Time `json:"sent_at"`
}

// NewID is a fresh message id.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ValidID reports whether s has the shape of a message or relay id.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// ValidAgentLabel reports whether s may label an agent.
func ValidAgentLabel(s string) bool { return agentPattern.MatchString(s) }

// Validate checks every field against the limits above.
func (m Message) Validate() error {
	switch {
	case m.V != 1:
		return fmt.Errorf("message version %d, want 1", m.V)
	case !ValidID(m.ID):
		return fmt.Errorf("message id %q is not 32 lowercase hex", clip(m.ID))
	case !ValidID(m.Thread):
		return fmt.Errorf("thread id %q is not 32 lowercase hex", clip(m.Thread))
	case m.ReplyTo != "" && !ValidID(m.ReplyTo):
		return fmt.Errorf("reply_to %q is not 32 lowercase hex", clip(m.ReplyTo))
	case !ValidAgentLabel(m.Agent):
		return fmt.Errorf("agent label %q may hold only letters, digits, '.', '_' and '-', at most 64", clip(m.Agent))
	case len(m.Subject) > MaxSubjectBytes:
		return fmt.Errorf("subject is %d bytes, at most %d", len(m.Subject), MaxSubjectBytes)
	case m.Body == "":
		return fmt.Errorf("message body is empty")
	case len(m.Body) > MaxBodyBytes:
		return fmt.Errorf("message body is %d bytes, at most %d", len(m.Body), MaxBodyBytes)
	case m.SentAt.IsZero():
		return fmt.Errorf("message has no sent_at")
	}
	return nil
}
