// Package progstatus implements the terminal side of the Program Status
// Protocol, OSC 7501, revision 0.2 (2026-10-06):
// https://www.superlogical.com/rex/docs/build/program-status
//
// A program writes OSC 7501 ; key=value:key=value ST to say what it is doing
// (idle, working, done, blocked or error), and the terminal keeps one record
// per id. This package holds the parts every user of the protocol shares: the
// parser, which applies every discard rule before anything is stored; the
// record store, with its limits, eviction, id hierarchy and lifetime events;
// the summary rule that picks the record a pane shows; and the encoder the
// tuios status command and the host forwarding use.
//
// It imports nothing from tuios, so the emulator, the daemon and the client
// can all use it.
package progstatus

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Command is the OSC number of the protocol.
const Command = 7501

// Limits from the specification's Limits section. A report that breaks one is
// discarded whole.
const (
	// MaxSequence is the most bytes of one sequence, OSC through ST.
	MaxSequence = 4096
	// MaxKey is the longest key, in bytes.
	MaxKey = 16
	// MaxMsgEncoded and MaxMsgDecoded bound msg before and after base64.
	MaxMsgEncoded = 2732
	MaxMsgDecoded = 2048
	// MaxTitleEncoded and MaxTitleDecoded bound title before and after base64.
	MaxTitleEncoded = 256
	MaxTitleDecoded = 192
	// MaxApp is the longest app value, in bytes.
	MaxApp = 32
	// MaxID, MaxSegment and MaxDepth bound an id.
	MaxID      = 128
	MaxSegment = 32
	MaxDepth   = 8
	// MaxRecords is how many records one terminal holds. The specification
	// requires at least 64 and caps it at 256.
	MaxRecords = 256
)

// State is the state key of a report.
type State string

// The states of the specification. Clear is not a state a record can hold:
// it removes records.
const (
	Idle    State = "idle"
	Working State = "working"
	Done    State = "done"
	Blocked State = "blocked"
	Error   State = "error"
	Clear   State = "clear"
)

// Kind is what a blocked record waits for.
type Kind string

// The kinds of the specification.
const (
	Permission Kind = "permission"
	Question   Kind = "question"
	Auth       Kind = "auth"
)

// NoProgress is the Progress of a report or record that carries none.
const NoProgress = -1

// Report is one OSC 7501 report that passed every check.
type Report struct {
	State State
	// ID is the record's id, empty for the root record.
	ID string
	// Kind is set only on a blocked report with a recognised kind.
	Kind Kind
	// Progress is 0 to 100 on a working or blocked report that carried a
	// valid one, and NoProgress otherwise.
	Progress int
	// App is the program's own name, empty when absent or invalid.
	App string
	// Title and Msg are the decoded texts, empty when absent.
	Title string
	Msg   string
}

// Event is what an emulator hands its owner: a report, or a full reset (RIS),
// which removes every record.
type Event struct {
	Report Report
	Reset  bool
}

// QueryReply is the only thing a terminal writes back, without its ST.
const QueryReply = "\x1b]7501;?"

// IsQuery reports whether body, the bytes after "7501;", is the feature
// detection query.
func IsQuery(body []byte) bool {
	return trimBlank(string(body)) == "?"
}

// trimBlank removes the whitespace the specification means around a key or a
// value: ASCII space and tab. Not unicode.IsSpace: a value never holds more
// than ASCII, and NBSP or U+0085 around one is a byte outside the value set,
// which makes the pair malformed rather than something to trim.
func trimBlank(s string) string {
	return strings.Trim(s, " \t")
}

// SequenceLen is the length of a whole sequence, OSC through ST, whose payload
// (the bytes between OSC and ST, "7501;..." included) is payloadLen bytes and
// which ended with BEL when bel is true.
func SequenceLen(payloadLen int, bel bool) int {
	n := 2 + payloadLen + 2 // ESC ] ... ESC \
	if bel {
		n--
	}
	return n
}

// Parse reads the body of a report, the bytes after "7501;", from a sequence
// seqLen bytes long. It returns false when the report is discarded or ignored;
// nothing from such a report may be applied. Every pair is checked before the
// report is returned, so a caller never applies half of one.
func Parse(body []byte, seqLen int) (Report, bool) {
	r := Report{Progress: NoProgress}
	if seqLen > MaxSequence || len(body) > MaxSequence {
		return r, false
	}
	// The last value of a repeated key wins, so values are gathered first and
	// checked once, as the report's own.
	var (
		state, id, kind, progress, app, title, msg string
		hasID, hasTitle, hasMsg                    bool
	)
	for pair := range strings.SplitSeq(string(body), ":") {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			continue // no "=": malformed, skipped
		}
		key := trimBlank(pair[:eq])
		value := trimBlank(pair[eq+1:])
		if len(key) > MaxKey {
			return r, false // a limit: the whole report goes
		}
		if !validKey(key) {
			continue // malformed: skipped
		}
		if !validValue(value) {
			if key == "id" {
				// An id outside the grammar ignores the report. Skipping the
				// pair would land the report on the root record, which is
				// what the specification says a malformed id must not do.
				return r, false
			}
			continue // malformed: skipped
		}
		switch key {
		case "state":
			state = value
		case "id":
			id, hasID = value, true
		case "kind":
			kind = value
		case "progress":
			progress = value
		case "app":
			app = value
		case "title":
			title, hasTitle = value, true
		case "msg":
			msg, hasMsg = value, true
		}
		// Any other key is unknown and ignored: that is how the protocol is
		// extended.
	}

	switch State(state) {
	case Idle, Working, Done, Blocked, Error, Clear:
		r.State = State(state)
	default:
		return r, false // no state, or one this terminal does not know
	}
	if hasID {
		if !ValidID(id) {
			return r, false // never falls back to the root record
		}
		r.ID = id
	}
	if len(app) > MaxApp {
		return r, false
	}
	if ValidApp(app) {
		r.App = app
	}
	if hasTitle {
		t, ok := decodeText(title, MaxTitleEncoded, MaxTitleDecoded)
		if !ok {
			return r, false
		}
		r.Title = t
	}
	if hasMsg {
		m, ok := decodeText(msg, MaxMsgEncoded, MaxMsgDecoded)
		if !ok {
			return r, false
		}
		r.Msg = m
	}
	if r.State == Blocked {
		switch Kind(kind) {
		case Permission, Question, Auth:
			r.Kind = Kind(kind)
		}
	}
	if r.State == Working || r.State == Blocked {
		r.Progress = parseProgress(progress)
	}
	if r.State == Clear {
		// A clear carries nothing but the id it addresses.
		r = Report{State: Clear, ID: r.ID, Progress: NoProgress}
	}
	return r, true
}

// validKey is the key grammar, [a-z]+.
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

// validValue is the value grammar, [A-Za-z0-9_.,+/=-]*.
func validValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == '.' || c == ',' || c == '+' || c == '/' || c == '=' || c == '-':
		default:
			return false
		}
	}
	return true
}

// segmentByte is the segment character set, [A-Za-z0-9_.+-], which app
// shares.
func segmentByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '_' || c == '.' || c == '+' || c == '-'
}

// ValidID reports whether id matches the id grammar and its limits: one to
// eight segments of 1 to 32 bytes of [A-Za-z0-9_.+-], joined with "/", at
// most 128 bytes in all. The root record has no id, so "" is not valid here.
func ValidID(id string) bool {
	if id == "" || len(id) > MaxID {
		return false
	}
	depth := 0
	for seg := range strings.SplitSeq(id, "/") {
		depth++
		if depth > MaxDepth || seg == "" || len(seg) > MaxSegment {
			return false
		}
		for i := 0; i < len(seg); i++ {
			if !segmentByte(seg[i]) {
				return false
			}
		}
	}
	return true
}

// ValidApp reports whether app matches the app grammar, [A-Za-z0-9_.+-]{1,32}.
func ValidApp(app string) bool {
	if app == "" || len(app) > MaxApp {
		return false
	}
	for i := 0; i < len(app); i++ {
		if !segmentByte(app[i]) {
			return false
		}
	}
	return true
}

// parseProgress reads progress: an integer 0 to 100, NoProgress for anything
// else.
func parseProgress(v string) int {
	if v == "" || len(v) > 3 {
		return NoProgress
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return NoProgress
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n > 100 {
		return NoProgress
	}
	return n
}

// decodeText decodes a base64 free-text value. The encoded size is checked
// before decoding. It fails on bad base64, on decoded text over maxDecoded
// bytes, on text that is not UTF-8, and on any control character.
func decodeText(v string, maxEncoded, maxDecoded int) (string, bool) {
	if len(v) > maxEncoded {
		return "", false
	}
	// Padding is optional. A value without it is padded here; a value with
	// it must have it right. Strict decoding refuses non-zero bits in the
	// unused tail, so each text has exactly one encoding.
	if !strings.Contains(v, "=") {
		switch len(v) % 4 {
		case 2:
			v += "=="
		case 3:
			v += "="
		}
	}
	b, err := base64.StdEncoding.Strict().DecodeString(v)
	if err != nil || len(b) > maxDecoded || !utf8.Valid(b) {
		return "", false
	}
	s := string(b)
	for _, r := range s {
		if IsControl(r) {
			return "", false
		}
	}
	return s, true
}

// IsControl reports whether r is a control character as the specification
// defines one: U+0000 to U+001F, U+007F and U+0080 to U+009F.
func IsControl(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f)
}

// Urgency orders states for the summary: the higher, the more a person needs
// to see it.
func Urgency(s State) int {
	switch s {
	case Blocked:
		return 5
	case Error:
		return 4
	case Done:
		return 3
	case Working:
		return 2
	case Idle:
		return 1
	}
	return 0
}

// Record is what the store keeps for one id.
type Record struct {
	ID       string
	State    State
	Kind     Kind
	Progress int
	// App is the record's own app. EffectiveApp is it or the nearest
	// ancestor's, filled in by Records.
	App          string
	EffectiveApp string
	Title        string
	Msg          string
	// Seq orders updates: the record updated least recently has the lowest.
	Seq uint64
	// At is when the record was last replaced, in Unix nanoseconds.
	At int64
}

// IsRoot reports whether r is the root record.
func (r Record) IsRoot() bool { return r.ID == "" }

// Store holds one terminal's records. Its zero value is empty and ready. It is
// safe for concurrent use, and it never calls out while it holds its lock.
type Store struct {
	mu   sync.Mutex
	recs map[string]*Record
	seq  uint64
	// seen is set by any report and cleared by a full reset. It is what stops
	// OSC 9;4 being read as program status once the program speaks for itself.
	seen bool
	// limit overrides MaxRecords in tests. Zero means MaxRecords.
	limit int
}

// Handle applies an emulator event at now (Unix nanoseconds) and reports
// whether the records changed.
func (s *Store) Handle(ev Event, now int64) bool {
	if ev.Reset {
		return s.Reset()
	}
	return s.Apply(ev.Report, now)
}

// Apply stores one report that Parse returned, and reports whether the
// records changed. A report replaces its record whole. A clear removes the
// addressed record and every record beneath it, and with no id removes every
// record.
func (s *Store) Apply(r Report, now int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = true
	if r.State == Clear {
		return s.clearLocked(r.ID)
	}
	if s.recs == nil {
		s.recs = make(map[string]*Record)
	}
	if _, ok := s.recs[r.ID]; !ok {
		limit := s.limit
		if limit <= 0 {
			limit = MaxRecords
		}
		for len(s.recs) >= limit {
			s.evictLocked()
		}
	}
	s.seq++
	s.recs[r.ID] = &Record{
		ID: r.ID, State: r.State, Kind: r.Kind, Progress: r.Progress,
		App: r.App, Title: r.Title, Msg: r.Msg, Seq: s.seq, At: now,
	}
	return true
}

// evictLocked removes the record updated least recently.
func (s *Store) evictLocked() {
	var oldest *Record
	for _, rec := range s.recs {
		if oldest == nil || rec.Seq < oldest.Seq {
			oldest = rec
		}
	}
	if oldest != nil {
		delete(s.recs, oldest.ID)
	}
}

func (s *Store) clearLocked(id string) bool {
	if id == "" {
		changed := len(s.recs) > 0
		s.recs = nil
		return changed
	}
	changed := false
	prefix := id + "/"
	for k := range s.recs {
		if k == id || strings.HasPrefix(k, prefix) {
			delete(s.recs, k)
			changed = true
		}
	}
	return changed
}

// DropTransient is what a shell prompt (OSC 133 A) and the exit of the
// terminal's process do: working and blocked records go, and so do idle ones,
// which the specification allows. done and error stay.
func (s *Store) DropTransient() bool {
	return s.drop(func(st State) bool { return st == Working || st == Blocked || st == Idle })
}

// DropFinished removes done and error records. tuios calls it when the person
// types into the pane, which is when the specification suggests a terminal
// stops showing them.
func (s *Store) DropFinished() bool {
	return s.drop(func(st State) bool { return st == Done || st == Error })
}

func (s *Store) drop(match func(State) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for k, rec := range s.recs {
		if match(rec.State) {
			delete(s.recs, k)
			changed = true
		}
	}
	return changed
}

// Reset is a full reset (RIS): every record goes, and OSC 9;4 may be read
// again. A soft reset (DECSTR) does not call it.
func (s *Store) Reset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := len(s.recs) > 0
	s.recs = nil
	s.seen = false
	return changed
}

// Seen reports whether a report has arrived since the last full reset.
func (s *Store) Seen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen
}

// Len is how many records the store holds.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recs)
}

// HasFinished reports whether a done or error record is held.
func (s *Store) HasFinished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.recs {
		if rec.State == Done || rec.State == Error {
			return true
		}
	}
	return false
}

// Records returns a copy of every record, the root first and the rest in id
// order, with EffectiveApp filled in from the nearest ancestor that has an
// app.
func (s *Store) Records() []Record {
	s.mu.Lock()
	out := make([]Record, 0, len(s.recs))
	for _, rec := range s.recs {
		out = append(out, *rec)
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
	InheritApp(out)
	return out
}

// InheritApp fills EffectiveApp on every record of recs from the record's own
// app or its nearest ancestor's. A parent does not have to exist, so the
// search walks up the id and skips missing levels.
func InheritApp(recs []Record) {
	byID := make(map[string]string, len(recs))
	for _, r := range recs {
		byID[r.ID] = r.App
	}
	for i := range recs {
		app := recs[i].App
		id := recs[i].ID
		for app == "" && id != "" {
			if j := strings.LastIndexByte(id, '/'); j >= 0 {
				id = id[:j]
			} else {
				id = ""
			}
			app = byID[id]
		}
		recs[i].EffectiveApp = app
	}
}

// Summary picks the record a pane shows: the most urgent state wins (blocked,
// then error, done, working, idle). Among records of the same urgency the
// root wins, and after it the one updated most recently. It reports false for
// no records.
func Summary(recs []Record) (Record, bool) {
	best := -1
	for i, r := range recs {
		if best < 0 {
			best = i
			continue
		}
		b := recs[best]
		ur, ub := Urgency(r.State), Urgency(b.State)
		switch {
		case ur > ub:
			best = i
		case ur < ub:
		case b.IsRoot():
		case r.IsRoot() || r.Seq > b.Seq:
			best = i
		}
	}
	if best < 0 {
		return Record{}, false
	}
	return recs[best], true
}

// Encode builds the body of a report for r, without OSC and ST: the pairs
// joined with ":", title and msg in standard base64. It does not check r;
// Valid does.
func Encode(r Report) string {
	pairs := []string{"state=" + string(r.State)}
	if r.ID != "" {
		pairs = append(pairs, "id="+r.ID)
	}
	if r.State == Clear {
		return strings.Join(pairs, ":")
	}
	if r.Kind != "" && r.State == Blocked {
		pairs = append(pairs, "kind="+string(r.Kind))
	}
	if r.Progress >= 0 && r.Progress <= 100 && (r.State == Working || r.State == Blocked) {
		pairs = append(pairs, "progress="+strconv.Itoa(r.Progress))
	}
	if r.App != "" {
		pairs = append(pairs, "app="+r.App)
	}
	if r.Title != "" {
		pairs = append(pairs, "title="+base64.StdEncoding.EncodeToString([]byte(r.Title)))
	}
	if r.Msg != "" {
		pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(r.Msg)))
	}
	return strings.Join(pairs, ":")
}

// Sequence is Encode wrapped in OSC 7501 and ESC \.
func Sequence(r Report) string {
	return "\x1b]7501;" + Encode(r) + "\x1b\\"
}

// FitText makes s fit a free-text value: control characters become spaces,
// invalid UTF-8 is dropped, and the text is cut on a rune boundary to max
// bytes (limit). A producer uses it so a report it builds is never discarded.
func FitText(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError {
			continue
		}
		if IsControl(r) {
			r = ' '
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FitSegment makes s a valid id segment or app value: bytes outside
// [A-Za-z0-9_.+-] become "-", and the result is cut to max bytes. An empty
// result becomes "-".
func FitSegment(s string, limit int) string {
	b := []byte(s)
	for i := range b {
		if !segmentByte(b[i]) {
			b[i] = '-'
		}
	}
	if len(b) > limit {
		b = b[:limit]
	}
	if len(b) == 0 {
		return "-"
	}
	return string(b)
}
