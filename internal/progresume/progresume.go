// Package progresume reads and writes the Program Resume Protocol (OSC 7577),
// revision 0.5.
//
// A program tells its terminal how to bring it back after the terminal
// restarts: an argument list, an owning pid, the time, and a hash that proves
// the program knows a token the terminal put in the environment of the
// pane's first process (OSC7577_TOKEN). The terminal checks the hash and the
// time before anything else, so text that only passes through a pane (a file
// shown with cat, the output of a remote host) cannot make a record.
//
// Parse is the terminal side, Sequence the program side. Neither keeps any
// state: the record store, the owner check and the order of reports for one
// pid are the caller's.
package progresume

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Command is the OSC number of the protocol.
const Command = 7577

// TokenEnv is the variable that holds a pane's token. A program that finds it
// may report, and a program that does not has no token to sign with.
const TokenEnv = "OSC7577_TOKEN"

// MaxSequence is the longest report accepted, in bytes, counted over the
// whole sequence from the OSC opener to the terminator. It is the minimum
// the specification asks a terminal to accept.
const MaxSequence = 2048

// MaxArgv is the most argv elements accepted, the minimum the specification
// asks for.
const MaxArgv = 64

// maxHeld is the longest payload Parse reads at all. A longer one is treated
// as a report the parser could not hold, which is ignored, so a flood of long
// sequences costs no hash.
const maxHeld = 16 << 10

// Window is how far a report's time may be from the terminal's clock.
const Window = 60 * time.Second

// tokenBytes is the randomness in a token: 24 bytes are 32 characters of
// base64url, 192 bits.
const tokenBytes = 24

// NewToken returns a fresh token of 32 characters of [A-Za-z0-9_-] from the
// system's secure random source.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidToken reports whether tok has the form the specification allows: 22
// to 64 characters of [A-Za-z0-9_-].
func ValidToken(tok string) bool {
	if len(tok) < 22 || len(tok) > 64 {
		return false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if !isAlnum(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// Report is one report, decoded.
type Report struct {
	// PID is the owning process. 0 when the report named no usable pid.
	PID int
	// Time is when the report was made, in seconds since the Unix epoch.
	Time int64
	// Argv is the resume command. Empty for a clear.
	Argv []string
	// Cwd is the folder to resume in, "" when the report named none.
	Cwd string
	// Clear asks to remove the owner's record.
	Clear bool
}

// Outcome is what a terminal does with a report.
type Outcome int

const (
	// Ignored has no effect at all: the mac or the time is wrong, or the
	// report is cut short.
	Ignored Outcome = iota
	// Rejected passed the mac and the time but breaks a rule. The owner's
	// records are removed, when the report names a usable pid.
	Rejected
	// Accepted passed every check of the protocol.
	Accepted
)

// Result is a parsed report and what to do with it. Report.PID and
// Report.Time are set for a rejected report when it named them.
type Result struct {
	Outcome Outcome
	Report  Report
	// Reason says why a report was ignored or rejected, for a debug log. It
	// never holds a value of the report.
	Reason string
}

// Implemented is every key this package reads, which is what a report's need
// key may name.
var Implemented = []string{"pid", "time", "mac", "argv", "cwd", "clear", "need"}

// Mac is the hex SHA-256 of body, ":" and token.
func Mac(body, token string) string {
	h := sha256.New()
	h.Write([]byte(body))
	h.Write([]byte{':'})
	h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}

// Parse reads the payload of one OSC 7577 sequence, "7577;" and the body, as
// the emulator hands it over. token is the pane's token and now the
// terminal's clock. The order is the specification's: the mac, then the time,
// then the grammar. A report whose time is earlier than the last accepted for
// its pid must be ignored as well; the caller holds those times.
func Parse(payload []byte, token string, now time.Time) Result {
	const prefix = "7577;"
	body, ok := bytes.CutPrefix(payload, []byte(prefix))
	if !ok {
		return ignored("not an OSC 7577 payload")
	}
	if len(payload) > maxHeld {
		return ignored("longer than the parser holds")
	}
	if string(body) == "?" {
		return ignored("reserved body")
	}
	if !ValidToken(token) {
		return ignored("no token")
	}
	i := bytes.LastIndex(body, []byte(":mac="))
	if i < 0 {
		return ignored("no mac")
	}
	signed, mac := body[:i], body[i+len(":mac="):]
	if len(mac) != sha256.Size*2 || !lowerHex(mac) {
		return ignored("the mac is not the last key or is malformed")
	}
	want := Mac(string(signed), token)
	if subtle.ConstantTimeCompare([]byte(want), mac) != 1 {
		return ignored("the mac does not match")
	}

	pairs := strings.Split(string(signed), ":")
	tval, found := firstValue(pairs, "time")
	if !found {
		return ignored("no time")
	}
	t, ok := positive(tval)
	if !ok {
		return ignored("the time cannot be read")
	}
	if d := now.Unix() - t; d > int64(Window/time.Second) || d < -int64(Window/time.Second) {
		return ignored("the time is outside the window")
	}

	res := Result{Outcome: Rejected, Report: Report{Time: t}}
	if pv, found := firstValue(pairs, "pid"); found && count(pairs, "pid") == 1 {
		if pid, ok := positive(pv); ok && pid <= int64(maxPID) {
			res.Report.PID = int(pid)
		}
	}
	if 2+len(payload)+1 > MaxSequence {
		res.Reason = "longer than the limit"
		return res
	}

	seen := make(map[string]bool, len(pairs))
	hasArgv := false
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || !validKey(k) || !validValue(v) {
			res.Reason = "a pair is malformed"
			return res
		}
		if seen[k] {
			res.Reason = "a key repeats"
			return res
		}
		seen[k] = true
		switch k {
		case "pid":
			if res.Report.PID == 0 {
				res.Reason = "the pid cannot be read"
				return res
			}
		case "time":
		case "mac":
			res.Reason = "a key repeats"
			return res
		case "argv":
			argv, why := decodeArgv(v)
			if why != "" {
				res.Reason = why
				return res
			}
			res.Report.Argv, hasArgv = argv, true
		case "cwd":
			cwd, ok := decodeText(v)
			if !ok || cwd == "" {
				res.Reason = "the cwd cannot be decoded"
				return res
			}
			if !strings.HasPrefix(cwd, "/") {
				res.Reason = "the cwd is not an absolute path"
				return res
			}
			res.Report.Cwd = cwd
		case "clear":
			if v != "1" {
				res.Reason = "clear is not 1"
				return res
			}
			res.Report.Clear = true
		case "need":
			if why := checkNeed(v); why != "" {
				res.Reason = why
				return res
			}
		}
	}
	switch {
	case !seen["pid"]:
		res.Reason = "no pid"
		return res
	case hasArgv == res.Report.Clear:
		res.Reason = "not exactly one of argv and clear"
		return res
	case res.Report.Clear && res.Report.Cwd != "":
		// A clear carries nothing else that means anything. The cwd is
		// dropped rather than refused: the rules name no conflict for it.
		res.Report.Cwd = ""
	}
	res.Outcome, res.Reason = Accepted, ""
	return res
}

// maxPID is the largest pid Parse names as an owner. A larger one is a pid
// the terminal cannot represent, which the specification reads as no owner.
const maxPID = 1<<31 - 1

func ignored(why string) Result { return Result{Outcome: Ignored, Reason: why} }

// firstValue is the value of the first pair with key k.
func firstValue(pairs []string, k string) (string, bool) {
	for _, p := range pairs {
		if key, v, ok := strings.Cut(p, "="); ok && key == k {
			return v, true
		}
	}
	return "", false
}

// count is how many pairs have key k.
func count(pairs []string, k string) int {
	n := 0
	for _, p := range pairs {
		if key, _, ok := strings.Cut(p, "="); ok && key == k {
			n++
		}
	}
	return n
}

// positive reads [1-9][0-9]* into an int64.
func positive(s string) (int64, bool) {
	if s == "" || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// decodeArgv reads the argv value: base64 elements joined by ",". The first
// is a bare command name that does not start with -, or an absolute path.
func decodeArgv(v string) ([]string, string) {
	parts := strings.Split(v, ",")
	if len(parts) > MaxArgv {
		return nil, "argv has more elements than the limit"
	}
	argv := make([]string, len(parts))
	for i, p := range parts {
		s, ok := decodeText(p)
		if !ok {
			return nil, "an argv element cannot be decoded"
		}
		argv[i] = s
	}
	first := argv[0]
	switch {
	case first == "":
		return nil, "argv has no program"
	case strings.HasPrefix(first, "/"):
	case strings.Contains(first, "/"):
		return nil, "the program is a relative path"
	case strings.HasPrefix(first, "-"):
		return nil, "the program starts with -"
	}
	return argv, ""
}

// decodeText decodes one base64 element of argv or cwd: standard base64,
// padding optional but right when present, UTF-8 text with no control
// characters.
func decodeText(p string) (string, bool) {
	enc := base64.RawStdEncoding
	if strings.Contains(p, "=") {
		enc = base64.StdEncoding
	}
	b, err := enc.Strict().DecodeString(p)
	if err != nil || !utf8.Valid(b) {
		return "", false
	}
	s := string(b)
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return "", false
		}
	}
	return s, true
}

// checkNeed reads a need value: key names joined by ",", each one this
// package implements.
func checkNeed(v string) string {
	for k := range strings.SplitSeq(v, ",") {
		if !validKey(k) {
			return "need is malformed"
		}
		known := false
		for _, have := range Implemented {
			if k == have {
				known = true
				break
			}
		}
		if !known {
			return "need names a key this terminal does not implement"
		}
	}
	return ""
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 'a' || k[i] > 'z' {
			return false
		}
	}
	return true
}

func validValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !isAlnum(c) && !strings.ContainsRune("_.,+/=-", rune(c)) {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func lowerHex(b []byte) bool {
	for _, c := range b {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Sequence is the whole OSC 7577 sequence for r, signed with token, ended
// with ST. A report with Clear set carries no argv or cwd.
func Sequence(r Report, token string) string {
	var b strings.Builder
	b.WriteString("pid=")
	b.WriteString(strconv.Itoa(r.PID))
	b.WriteString(":time=")
	b.WriteString(strconv.FormatInt(r.Time, 10))
	if r.Clear {
		b.WriteString(":clear=1")
	} else {
		b.WriteString(":argv=")
		for i, a := range r.Argv {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(base64.StdEncoding.EncodeToString([]byte(a)))
		}
		if r.Cwd != "" {
			b.WriteString(":cwd=")
			b.WriteString(base64.StdEncoding.EncodeToString([]byte(r.Cwd)))
		}
	}
	body := b.String()
	return "\x1b]7577;" + body + ":mac=" + Mac(body, token) + "\x1b\\"
}
