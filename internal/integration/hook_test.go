package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// hookCase is one fixture: a payload a harness hands its hooks, and the
// report it must come to, or a substring of the reason it reports nothing.
type hookCase struct {
	Name    string            `json:"name"`
	Event   string            `json:"event"`
	Env     map[string]string `json:"env"`
	Payload json.RawMessage   `json:"payload"`
	Raw     *string           `json:"raw"`
	Want    *Report           `json:"want"`
	Skip    string            `json:"skip"`
}

func loadHookCases(t *testing.T, harness string) []hookCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "hooks", harness+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []hookCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("%s fixtures: %v", harness, err)
	}
	return cases
}

// TestTranslateFixtures runs every fixture payload through Translate.
func TestTranslateFixtures(t *testing.T) {
	for _, harness := range HarnessIDs() {
		for _, tc := range loadHookCases(t, harness) {
			t.Run(harness+"/"+tc.Name, func(t *testing.T) {
				payload := []byte(tc.Payload)
				if tc.Raw != nil {
					payload = []byte(*tc.Raw)
				}
				got := Translate(harness, Input{
					Event:   tc.Event,
					Payload: payload,
					Getenv:  func(k string) string { return tc.Env[k] },
				})
				switch {
				case tc.Want != nil:
					if got.Report == nil {
						t.Fatalf("reported nothing (%s), want %+v", got.Skip, *tc.Want)
					}
					if !reflect.DeepEqual(*got.Report, *tc.Want) {
						gotJSON, _ := json.Marshal(got.Report)
						wantJSON, _ := json.Marshal(tc.Want)
						t.Fatalf("report = %s\nwant     %s", gotJSON, wantJSON)
					}
				case tc.Skip != "":
					if got.Report != nil {
						t.Fatalf("reported %+v, want nothing (%s)", *got.Report, tc.Skip)
					}
					if !strings.Contains(got.Skip, tc.Skip) {
						t.Fatalf("skip reason %q, want it to mention %q", got.Skip, tc.Skip)
					}
				default:
					t.Fatal("fixture has neither want nor skip")
				}
			})
		}
	}
}

// TestEveryInstalledEventHasAFixture holds the installers to the map: an event
// tuios registers a command for must have a payload fixture that reports
// something, or the installer is wiring an event nothing reads.
func TestEveryInstalledEventHasAFixture(t *testing.T) {
	for _, target := range Targets() {
		reported := map[string]bool{}
		for _, tc := range loadHookCases(t, target.ID) {
			var p map[string]any
			_ = json.Unmarshal(tc.Payload, &p)
			event := tc.Event
			if event == "" {
				event = fields(p).first("hook_event_name", "hookEventName", "event")
			}
			if tc.Want != nil {
				reported[event] = true
			}
		}
		for _, ev := range target.Events {
			if !reported[ev.Name] {
				t.Errorf("%s registers %s, and no fixture for it reports a state", target.ID, ev.Name)
			}
		}
	}
}

// TestTranslateNeverReportsDoneOnFailure is the rule the old shim broke in
// spirit: whatever goes wrong reading the payload, the answer is no report.
func TestTranslateNeverReportsDoneOnFailure(t *testing.T) {
	for _, harness := range HarnessIDs() {
		for _, raw := range []string{"{", "null", "[]", `"Stop"`, "\x00\x01"} {
			for _, event := range []string{"", "Stop", "AfterAgent", "session.idle"} {
				got := Translate(harness, Input{Event: event, Payload: []byte(raw)})
				if got.Report != nil {
					t.Errorf("%s %q %q reported %+v", harness, event, raw, *got.Report)
				}
			}
		}
	}
	if got := Translate("aider", Input{Event: "Stop"}); got.Report != nil {
		t.Errorf("a harness with no map reported %+v", *got.Report)
	}
}

func TestClipAndRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"export API_KEY=sk-live-123 && run", "export API_KEY=*** && run"},
		{"curl -H 'Authorization: Bearer abcdefghijklmnop' x", "curl -H 'Authorization: Bearer ***' x"},
		{"git clone https://user:hunter2@example.com/r.git", "git clone https://***@example.com/r.git"},
		{"mytool --token=abc123 --password s3cret", "mytool --token=*** --password ***"},
		{"echo 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", "echo ***"},
		// A long path built of words is a path, not a key.
		{"/home/user/dev/internal/session/agent_detect_cost_bench_test.go", "/home/user/dev/internal/session/agent_detect_cost_bench_test.go"},
		{"line one\n  line two\ttabbed", "line one line two tabbed"},
	}
	for _, tc := range cases {
		if got := Clip(tc.in); got != tc.want {
			t.Errorf("Clip(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := strings.Repeat("word ", 60)
	got := Clip(long)
	if len([]rune(got)) > MaxMessage || !strings.HasSuffix(got, "...") {
		t.Errorf("a long message was not cut to %d runes: %q", MaxMessage, got)
	}
}
