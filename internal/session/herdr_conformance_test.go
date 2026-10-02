//go:build !slim

package session

import (
	"bufio"
	"encoding/json"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// herdrConformanceStep is one request of a herdr client and what the client
// reads from the reply.
type herdrConformanceStep struct {
	Call   string            `json:"call"`
	Method string            `json:"method"`
	Params json.RawMessage   `json:"params"`
	Type   string            `json:"type"`
	Read   []string          `json:"read"`
	Error  string            `json:"error"`
	Save   map[string]string `json:"save"`
}

// TestHerdrConformanceCollie replays the requests Collie's herdr adapter
// sends (testdata/herdr/collie_requests.json, ported from Collie's
// bridge/mux/herdr/client.ts) against a daemon, over the herdr socket, and
// checks each reply has the result type and every field Collie reads.
func TestHerdrConformanceCollie(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "herdr", "collie_requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Steps []herdrConformanceStep `json:"steps"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	d, sp, repo := worktreeFixture(t)
	sess := makeSessionWithWindow(t, d, "collie")
	vars := map[string]string{
		"PANE": herdrPaneID(sess.ID, sess.GetState().Windows[0].ID),
		"WS":   herdrWorkspaceID(sess.ID),
		"CWD":  t.TempDir(),
		"REPO": repo,
	}
	for _, step := range fixture.Steps {
		params := string(step.Params)
		// Longer names first, so $TABPANE is not read as $TAB.
		names := slices.Collect(maps.Keys(vars))
		slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
		for _, k := range names {
			params = strings.ReplaceAll(params, "$"+k, vars[k])
		}
		reply := herdrRaw(t, sp, step.Method, params)
		if step.Error != "" {
			if code := herdrCode(reply); code != step.Error {
				t.Errorf("%s: code %q, want %q (%v)", step.Call, code, step.Error, reply)
			}
			continue
		}
		res, ok := reply["result"].(map[string]any)
		if !ok {
			t.Errorf("%s: %v", step.Call, reply)
			continue
		}
		if res["type"] != step.Type {
			t.Errorf("%s: type %v, want %s", step.Call, res["type"], step.Type)
		}
		for _, path := range step.Read {
			if !herdrHasPath(res, path) {
				t.Errorf("%s: no %s in %v", step.Call, path, res)
			}
		}
		for name, path := range step.Save {
			v, _ := herdrPath(res, path).(string)
			if v == "" {
				t.Fatalf("%s: nothing at %s to save as %s", step.Call, path, name)
			}
			vars[name] = v
		}
	}
}

// herdrRaw sends one request line with raw params and returns the first
// reply line, which for events.subscribe is the acknowledgement.
func herdrRaw(t *testing.T, sp, method, params string) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", HerdrSocketPath(sp))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(`{"id":"c1","method":"` + method + `","params":` + params + "}\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal(line, &out); err != nil {
		t.Fatalf("%s: %q: %v", method, line, err)
	}
	if out["id"] != "c1" {
		t.Errorf("%s: id %v", method, out["id"])
	}
	return out
}

// herdrPath reads a dotted path; a[] means every element of list a.
func herdrPath(v any, path string) any {
	if path == "" {
		return v
	}
	head, rest, _ := strings.Cut(path, ".")
	if name, ok := strings.CutSuffix(head, "[]"); ok {
		list, _ := v.(map[string]any)[name].([]any)
		return list
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return herdrPath(m[head], rest)
}

// herdrHasPath reports whether a path is present; for a list path, whether
// every element has the field. An empty list has it vacuously, and the
// fixture's lists are never empty here.
func herdrHasPath(v any, path string) bool {
	head, rest, _ := strings.Cut(path, ".")
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if name, isList := strings.CutSuffix(head, "[]"); isList {
		list, ok := m[name].([]any)
		if !ok || len(list) == 0 {
			return false
		}
		for _, el := range list {
			if rest != "" && !herdrHasPath(el, rest) {
				return false
			}
		}
		return true
	}
	val, ok := m[head]
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	return herdrHasPath(val, rest)
}
