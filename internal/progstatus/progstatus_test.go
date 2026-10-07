package progstatus

// The parser is a security boundary: every byte comes from a program that
// already controls the screen, and a report that should be discarded must
// leave no trace. The ways it could fail, and the cases below that pin each:
//
//   - a malformed pair is applied, or it takes the whole report with it
//   - a discard rule is skipped: oversize sequence, long key, bad base64,
//     decoded control character, missing or unknown state, bad id
//   - a limit is off by one: sequence, key, msg and title before and after
//     decoding, app, id length, segment length and depth
//   - an optional key that is invalid discards the report instead of being
//     treated as absent (kind, progress, app characters)
//   - a repeated key keeps the first value
//   - whitespace around a key or value is kept, or whitespace inside is
//     removed
//   - clear removes a sibling whose id only starts with the cleared id
//   - eviction removes something other than the least recently updated
//   - app inheritance stops at a missing parent
//   - the summary picks a less urgent record, or loses the root among equals

import (
	"encoding/base64"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// parse runs Parse on body as if it came in a sequence ended with ST.
func parse(body string) (Report, bool) {
	return Parse([]byte(body), SequenceLen(len("7501;")+len(body), false))
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
		want Report
	}{
		{"state only", "state=idle", true, Report{State: Idle, Progress: -1}},
		{"every key", "state=blocked:kind=auth:progress=7:app=brew:id=a/b:title=" + b64("T") + ":msg=" + b64("M"), true,
			Report{State: Blocked, Kind: Auth, Progress: 7, App: "brew", ID: "a/b", Title: "T", Msg: "M"}},
		{"no state", "app=cargo", false, Report{}},
		{"unknown state", "state=sleeping", false, Report{}},
		{"empty state", "state=", false, Report{}},
		{"state is case sensitive", "state=Idle", false, Report{}},
		{"empty body", "", false, Report{}},

		{"pair without = is skipped", "state=idle:junk", true, Report{State: Idle, Progress: -1}},
		{"empty key is skipped", "state=idle:=x", true, Report{State: Idle, Progress: -1}},
		{"upper case key is skipped", "state=idle:App=cargo", true, Report{State: Idle, Progress: -1}},
		{"value byte outside the set skips the pair", "state=idle:app=car go", true, Report{State: Idle, Progress: -1}},
		{"a skipped state pair leaves no state", "state=id le", false, Report{}},
		{"unknown key is ignored", "state=idle:colour=red", true, Report{State: Idle, Progress: -1}},
		{"last value wins", "state=working:state=done", true, Report{State: Done, Progress: -1}},
		{"last value wins even when invalid", "state=done:state=nope", false, Report{}},
		{"whitespace around keys and values", " state = working : app = cargo ", true, Report{State: Working, App: "cargo", Progress: -1}},
		{"tab around a value", "state=\tidle\t", true, Report{State: Idle, Progress: -1}},

		{"kind without blocked is ignored", "state=working:kind=auth", true, Report{State: Working, Progress: -1}},
		{"unknown kind is absent", "state=blocked:kind=coffee", true, Report{State: Blocked, Progress: -1}},
		{"progress 0", "state=working:progress=0", true, Report{State: Working, Progress: 0}},
		{"progress 100", "state=working:progress=100", true, Report{State: Working, Progress: 100}},
		{"progress 101 is absent", "state=working:progress=101", true, Report{State: Working, Progress: -1}},
		{"progress -1 is absent", "state=working:progress=-1", true, Report{State: Working, Progress: -1}},
		{"progress 4.5 is absent", "state=working:progress=4.5", true, Report{State: Working, Progress: -1}},
		{"progress +5 is absent", "state=working:progress=+5", true, Report{State: Working, Progress: -1}},
		{"progress on done is ignored", "state=done:progress=50", true, Report{State: Done, Progress: -1}},
		{"progress on blocked", "state=blocked:progress=50", true, Report{State: Blocked, Progress: 50}},

		{"app with a character outside its set is absent", "state=idle:app=a/b", true, Report{State: Idle, Progress: -1}},
		{"app of 32 bytes", "state=idle:app=" + strings.Repeat("a", 32), true, Report{State: Idle, App: strings.Repeat("a", 32), Progress: -1}},
		{"app of 33 bytes discards", "state=idle:app=" + strings.Repeat("a", 33), false, Report{}},

		{"id of 8 levels", "state=idle:id=a/b/c/d/e/f/g/h", true, Report{State: Idle, ID: "a/b/c/d/e/f/g/h", Progress: -1}},
		{"id of 9 levels discards", "state=idle:id=a/b/c/d/e/f/g/h/i", false, Report{}},
		{"id segment of 32", "state=idle:id=" + strings.Repeat("s", 32), true, Report{State: Idle, ID: strings.Repeat("s", 32), Progress: -1}},
		{"id segment of 33 discards", "state=idle:id=" + strings.Repeat("s", 33), false, Report{}},
		{"id of 128 bytes", "state=idle:id=" + longID(128), true, Report{State: Idle, ID: longID(128), Progress: -1}},
		{"id of 129 bytes discards", "state=idle:id=" + longID(129), false, Report{}},
		{"empty id discards", "state=idle:id=", false, Report{}},
		{"empty segment discards", "state=idle:id=a//b", false, Report{}},
		{"leading slash discards", "state=idle:id=/a", false, Report{}},
		{"id with = discards", "state=idle:id=a=b", false, Report{}},
		{"id with , discards", "state=idle:id=a,b", false, Report{}},
		{"id with a space inside discards", "state=idle:id=a b", false, Report{}},
		{"id with a non-ASCII letter discards", "state=idle:id=caf\u00e9", false, Report{}},
		{"id with ; discards", "state=idle:id=x;y", false, Report{}},
		{"a malformed id discards even before a valid one", "state=idle:id=a b:id=x", false, Report{}},
		{"NBSP around a value is not trimmed", "state=idle:app=\u00a0cargo", true, Report{State: Idle, Progress: -1}},
		{"NEL around a value is not trimmed", "state=idle:app=cargo\u0085", true, Report{State: Idle, Progress: -1}},

		{"padding is optional", "state=idle:msg=aGk", true, Report{State: Idle, Msg: "hi", Progress: -1}},
		{"padded", "state=idle:msg=aGk=", true, Report{State: Idle, Msg: "hi", Progress: -1}},
		{"bad base64 discards", "state=idle:msg=a", false, Report{}},
		{"padding in the middle discards", "state=idle:msg=aG=k", false, Report{}},
		{"three padding bytes discard", "state=idle:msg=aGk===", false, Report{}},
		{"too much padding discards", "state=idle:msg=aGk==", false, Report{}},
		{"a non-canonical tail discards", "state=idle:msg=aGl=", false, Report{}},
		{"a non-canonical tail without padding discards", "state=idle:msg=aGl", false, Report{}},
		{"a length of 4n+1 discards", "state=idle:msg=aGkhY", false, Report{}},
		{"empty msg", "state=idle:msg=", true, Report{State: Idle, Progress: -1}},
		{"newline in msg discards", "state=idle:msg=" + b64("a\nb"), false, Report{}},
		{"escape in title discards", "state=idle:title=" + b64("\x1b[31m"), false, Report{}},
		{"DEL discards", "state=idle:msg=" + b64("a\x7f"), false, Report{}},
		{"C1 control discards", "state=idle:msg=" + b64("a\u0085"), false, Report{}},
		{"CSI as C1 discards", "state=idle:msg=" + b64("\u009b31m"), false, Report{}},
		{"invalid UTF-8 discards", "state=idle:msg=" + b64("\xff"), false, Report{}},
		{"bidi override is kept for the display to disarm", "state=idle:msg=" + b64("a\u202eb"), true, Report{State: Idle, Msg: "a\u202eb", Progress: -1}},
		{"markup is text", "state=idle:msg=" + b64("<b>x</b>"), true, Report{State: Idle, Msg: "<b>x</b>", Progress: -1}},

		{"msg of 2048 bytes", "state=idle:msg=" + b64(strings.Repeat("m", 2048)), true, Report{State: Idle, Msg: strings.Repeat("m", 2048), Progress: -1}},
		{"msg of 2049 bytes discards", "state=idle:msg=" + b64(strings.Repeat("m", 2049)), false, Report{}},
		{"title of 192 bytes", "state=idle:title=" + b64(strings.Repeat("t", 192)), true, Report{State: Idle, Title: strings.Repeat("t", 192), Progress: -1}},
		{"title of 193 bytes discards", "state=idle:title=" + b64(strings.Repeat("t", 193)), false, Report{}},
		{"title over 256 encoded bytes discards before decoding", "state=idle:title=" + strings.Repeat("A", 260), false, Report{}},
		{"key of 16 bytes is an unknown key", "state=idle:" + strings.Repeat("k", 16) + "=1", true, Report{State: Idle, Progress: -1}},
		{"key of 17 bytes discards", "state=idle:" + strings.Repeat("k", 17) + "=1", false, Report{}},

		{"clear", "state=clear", true, Report{State: Clear, Progress: -1}},
		{"clear an id", "state=clear:id=a/b:app=x:msg=" + b64("m"), true, Report{State: Clear, ID: "a/b", Progress: -1}},
		{"clear with a bad id discards", "state=clear:id=a//b", false, Report{}},
		{"clear with bad base64 discards", "state=clear:msg=a", false, Report{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parse(tc.body)
			if ok != tc.ok {
				t.Fatalf("Parse(%q) ok = %v, want %v (%+v)", tc.body, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.body, got, tc.want)
			}
		})
	}
}

// longID is an id of exactly n bytes, in segments of 32 or fewer.
func longID(n int) string {
	// Segments of 30 bytes and a slash, then one last segment for the rest.
	var b strings.Builder
	for n-b.Len() > 31 {
		b.WriteString(strings.Repeat("x", 30) + "/")
	}
	b.WriteString(strings.Repeat("y", n-b.Len()))
	return b.String()
}

func TestParseSequenceLimit(t *testing.T) {
	// The whole sequence, OSC through ST, may be 4096 bytes and no more.
	body := "state=idle:x=" // an unknown key pads the report
	pad := MaxSequence - SequenceLen(len("7501;")+len(body), false)
	full := body + strings.Repeat("a", pad)
	if _, ok := Parse([]byte(full), SequenceLen(len("7501;")+len(full), false)); !ok {
		t.Errorf("a 4096-byte sequence was discarded")
	}
	over := full + "a"
	if _, ok := Parse([]byte(over), SequenceLen(len("7501;")+len(over), false)); ok {
		t.Errorf("a 4097-byte sequence was kept")
	}
	// BEL is one byte shorter than ESC \.
	if _, ok := Parse([]byte(over), SequenceLen(len("7501;")+len(over), true)); !ok {
		t.Errorf("a 4096-byte sequence ended with BEL was discarded")
	}
}

func TestSpecExamplesRoundTrip(t *testing.T) {
	// The examples of the specification, decoded.
	for body, want := range map[string]Report{
		"state=blocked:kind=permission:app=terraform:msg=QXBwbHkgMyB0byBhZGQsIDEgdG8gY2hhbmdlLCAwIHRvIGRlc3Ryb3k/": {
			State: Blocked, Kind: Permission, App: "terraform", Msg: "Apply 3 to add, 1 to change, 0 to destroy?", Progress: -1},
		"state=working:id=us-east:title=VVMgRWFzdA==:progress=40:msg=UHVzaGluZyBpbWFnZQ==": {
			State: Working, ID: "us-east", Title: "US East", Msg: "Pushing image", Progress: 40},
		"state=done:app=brew:msg=VXBncmFkZWQgMTIgcGFja2FnZXM=": {
			State: Done, App: "brew", Msg: "Upgraded 12 packages", Progress: -1},
	} {
		got, ok := parse(body)
		if !ok || got != want {
			t.Errorf("Parse(%q) = %+v, %v, want %+v", body, got, ok, want)
		}
		// Encode writes what Parse reads back.
		again, ok := parse(Encode(got))
		if !ok || again != got {
			t.Errorf("round trip of %+v = %+v, %v", got, again, ok)
		}
	}
}

func apply(t *testing.T, s *Store, body string) {
	t.Helper()
	r, ok := parse(body)
	if !ok {
		t.Fatalf("Parse(%q) failed", body)
	}
	s.Apply(r, 0)
}

func ids(recs []Record) string {
	var out []string
	for _, r := range recs {
		id := r.ID
		if id == "" {
			id = "<root>"
		}
		out = append(out, id+"="+string(r.State))
	}
	return strings.Join(out, " ")
}

func TestStoreReplaceAndClear(t *testing.T) {
	var s Store
	apply(t, &s, "state=working:app=deploy:msg="+b64("Deploying"))
	apply(t, &s, "state=working:id=build")
	apply(t, &s, "state=working:id=build/test")
	apply(t, &s, "state=working:id=builder")
	apply(t, &s, "state=blocked:id=build/test/unit")
	if got := ids(s.Records()); got != "<root>=working build=working build/test=working build/test/unit=blocked builder=working" {
		t.Fatalf("records = %s", got)
	}
	// A report replaces its record whole: the msg is gone.
	apply(t, &s, "state=working:app=deploy")
	if r := s.Records()[0]; r.Msg != "" {
		t.Errorf("root msg = %q after a report without one", r.Msg)
	}
	// clear removes the record and every record beneath it, and not a
	// sibling whose id only starts the same.
	apply(t, &s, "state=clear:id=build")
	if got := ids(s.Records()); got != "<root>=working builder=working" {
		t.Errorf("after clear build: %s", got)
	}
	// A record beneath a parent that does not exist is still cleared.
	apply(t, &s, "state=idle:id=x/y/z")
	apply(t, &s, "state=clear:id=x")
	if got := ids(s.Records()); got != "<root>=working builder=working" {
		t.Errorf("after clear x: %s", got)
	}
	apply(t, &s, "state=clear")
	if n := s.Len(); n != 0 {
		t.Errorf("clear with no id left %d records", n)
	}
}

func TestStoreLifetime(t *testing.T) {
	var s Store
	for _, st := range []string{"idle", "working", "done", "blocked", "error"} {
		apply(t, &s, "state="+st+":id="+st)
	}
	if !s.DropTransient() {
		t.Fatal("DropTransient changed nothing")
	}
	if got := ids(s.Records()); got != "done=done error=error" {
		t.Errorf("after a prompt: %s", got)
	}
	if s.DropTransient() {
		t.Error("a second DropTransient reported a change")
	}
	if !s.HasFinished() || !s.DropFinished() || s.Len() != 0 {
		t.Errorf("DropFinished left %s", ids(s.Records()))
	}
	apply(t, &s, "state=idle")
	if !s.Seen() {
		t.Error("Seen is false after a report")
	}
	s.Reset()
	if s.Seen() || s.Len() != 0 {
		t.Error("Reset left records or Seen")
	}
}

func TestStoreEviction(t *testing.T) {
	s := Store{limit: 3}
	apply(t, &s, "state=idle:id=a")
	apply(t, &s, "state=idle:id=b")
	apply(t, &s, "state=idle:id=c")
	// a is updated, so b is now the least recently updated.
	apply(t, &s, "state=working:id=a")
	apply(t, &s, "state=idle:id=d")
	if got := ids(s.Records()); got != "a=working c=idle d=idle" {
		t.Errorf("after eviction: %s", got)
	}
	// Replacing a record at the cap evicts nothing.
	apply(t, &s, "state=done:id=c")
	if got := ids(s.Records()); got != "a=working c=done d=idle" {
		t.Errorf("after a replace at the cap: %s", got)
	}
}

func TestStoreDefaultCap(t *testing.T) {
	var s Store
	for i := range MaxRecords + 10 {
		s.Apply(Report{State: Idle, ID: "r" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), Progress: -1}, 0)
	}
	if n := s.Len(); n != MaxRecords {
		t.Errorf("store holds %d records, want %d", n, MaxRecords)
	}
}

func TestInheritApp(t *testing.T) {
	var s Store
	apply(t, &s, "state=working:app=deploy")
	apply(t, &s, "state=working:id=eu-west/db/migrate") // parents missing
	apply(t, &s, "state=working:id=us-east:app=docker")
	apply(t, &s, "state=working:id=us-east/push")
	got := map[string]string{}
	for _, r := range s.Records() {
		got[r.ID] = r.EffectiveApp
	}
	for id, want := range map[string]string{"": "deploy", "eu-west/db/migrate": "deploy", "us-east": "docker", "us-east/push": "docker"} {
		if got[id] != want {
			t.Errorf("app of %q = %q, want %q", id, got[id], want)
		}
	}
}

func TestSummary(t *testing.T) {
	rec := func(id string, st State, seq uint64) Record { return Record{ID: id, State: st, Seq: seq} }
	for _, tc := range []struct {
		name string
		recs []Record
		want string
	}{
		{"blocked beats a working root", []Record{rec("", Working, 1), rec("eu", Blocked, 2)}, "eu"},
		{"error beats done", []Record{rec("a", Done, 3), rec("b", Error, 1)}, "b"},
		{"done beats working", []Record{rec("", Working, 3), rec("b", Done, 1)}, "b"},
		{"working beats idle", []Record{rec("", Idle, 3), rec("b", Working, 1)}, "b"},
		{"the root wins among equals", []Record{rec("a", Working, 9), rec("", Working, 1)}, ""},
		{"then the newest", []Record{rec("a", Working, 1), rec("b", Working, 2)}, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Summary(tc.recs)
			if !ok || got.ID != tc.want {
				t.Errorf("Summary = %q, want %q", got.ID, tc.want)
			}
		})
	}
	if _, ok := Summary(nil); ok {
		t.Error("Summary of nothing reported a record")
	}
}

// FuzzParse checks the parser against its own contract on any input: it
// never panics, a report it accepts holds only values that pass every rule,
// and encoding that report and parsing it again gives the same report.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"state=idle",
		"state=blocked:kind=permission:app=terraform:msg=QXBwbHk/",
		"state=working:id=us-east:title=VVMgRWFzdA==:progress=40",
		"state=clear:id=a/b",
		" state = done : app = x ",
		"state=idle:msg=aGk",
		"state=idle:id=a//b",
		"?",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		r, ok := Parse(body, SequenceLen(len("7501;")+len(body), false))
		if !ok {
			return
		}
		if len(body)+len("7501;")+4 > MaxSequence {
			t.Fatalf("kept a report of %d bytes", len(body))
		}
		// The discard rules, worked out again from the raw pairs: a key over
		// the limit, a malformed id anywhere, or a last id outside the grammar
		// must each have thrown the report away.
		lastID, hasID := "", false
		for pair := range strings.SplitSeq(string(body), ":") {
			k, v, found := strings.Cut(pair, "=")
			if !found {
				continue
			}
			k, v = strings.Trim(k, " \t"), strings.Trim(v, " \t")
			if len(k) > MaxKey {
				t.Fatalf("kept a report with a %d-byte key: %q", len(k), body)
			}
			if k == "id" {
				if !validValue(v) {
					t.Fatalf("kept a report with the malformed id %q: %q", v, body)
				}
				lastID, hasID = v, true
			}
		}
		if hasID && (!ValidID(lastID) || r.ID != lastID) {
			t.Fatalf("kept id %q as %q", lastID, r.ID)
		}
		switch r.State {
		case Idle, Working, Done, Blocked, Error, Clear:
		default:
			t.Fatalf("kept state %q", r.State)
		}
		if r.ID != "" && !ValidID(r.ID) {
			t.Fatalf("kept id %q", r.ID)
		}
		if r.App != "" && !ValidApp(r.App) {
			t.Fatalf("kept app %q", r.App)
		}
		if len(r.Title) > MaxTitleDecoded || len(r.Msg) > MaxMsgDecoded {
			t.Fatalf("kept a title of %d or a msg of %d bytes", len(r.Title), len(r.Msg))
		}
		for _, c := range r.Title + r.Msg {
			if IsControl(c) {
				t.Fatalf("kept control character %U", c)
			}
		}
		if r.Kind != "" && r.State != Blocked {
			t.Fatalf("kept kind %q on %q", r.Kind, r.State)
		}
		if r.Progress != NoProgress && (r.Progress < 0 || r.Progress > 100 || (r.State != Working && r.State != Blocked)) {
			t.Fatalf("kept progress %d on %q", r.Progress, r.State)
		}
		enc := Encode(r)
		again, ok := Parse([]byte(enc), SequenceLen(len("7501;")+len(enc), false))
		if !ok || again != r {
			t.Fatalf("round trip of %+v gave %+v, %v", r, again, ok)
		}
	})
}
