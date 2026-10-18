package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestViewSearchSiblings checks which file the hookless search gives each of
// several agents of one harness in one folder. A wrong answer joins a pane to
// a sibling's conversation and keeps it there, which shows one agent's work
// as another's. Each file's modification time is after every agent started,
// as it is for agents that all keep writing, so a rule that went by the
// modification time could not tell them apart.
func TestViewSearchSiblings(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	const cwd = "/work/project"
	type file struct {
		name  string
		start time.Duration
	}
	type pane struct {
		name    string
		started time.Duration
		joined  string
		exact   bool
	}
	cases := []struct {
		name  string
		files []file
		panes []pane
		taken []string
		want  map[string]string
	}{
		{
			name:  "each agent its own session",
			files: []file{{"a", time.Second}, {"b", 5*time.Minute + time.Second}},
			panes: []pane{{name: "A"}, {name: "B", started: 5 * time.Minute}},
			want:  map[string]string{"A": "a", "B": "b"},
		},
		{
			name:  "the second agent has not written yet",
			files: []file{{"a", time.Second}},
			panes: []pane{{name: "A"}, {name: "B", started: 5 * time.Minute}},
			want:  map[string]string{"A": "a"},
		},
		{
			name:  "two agents started together",
			files: []file{{"a", time.Second}, {"b", 11 * time.Second}},
			panes: []pane{{name: "A"}, {name: "B", started: 10 * time.Second}},
			// b started near both agents, so it is no one's.
			want: map[string]string{"A": "a"},
		},
		{
			name:  "a sibling's hook named its own file",
			files: []file{{"a", time.Second}, {"b", 11 * time.Second}},
			panes: []pane{{name: "A", joined: "a", exact: true}, {name: "B", started: 10 * time.Second}},
			want:  map[string]string{"A": "a", "B": "b"},
		},
		{
			name:  "a file another pane joined",
			files: []file{{"a", time.Second}},
			panes: []pane{{name: "A"}},
			taken: []string{"a"},
			want:  map[string]string{},
		},
		{
			name:  "a newer session of the same agent",
			files: []file{{"a", time.Second}, {"a2", 30 * time.Second}},
			panes: []pane{{name: "A"}},
			want:  map[string]string{"A": "a2"},
		},
		{
			name:  "a session started long after the agent",
			files: []file{{"late", 10 * time.Minute}},
			panes: []pane{{name: "A"}},
			want:  map[string]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			tr := &harness.Transcript{Reader: "codex-rollout", Dir: dir, Glob: "*.jsonl", Verify: []string{"cwd"}}
			late := t0.Add(time.Hour)
			path := func(name string) string { return filepath.Join(dir, "rollout-"+name+".jsonl") }
			for _, f := range c.files {
				meta, _ := json.Marshal(map[string]any{"timestamp": t0.Add(f.start).Format(time.RFC3339Nano), "type": "session_meta",
					"payload": map[string]any{"id": f.name, "timestamp": t0.Add(f.start).Format(time.RFC3339Nano), "cwd": cwd}})
				if err := os.WriteFile(path(f.name), append(meta, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path(f.name), late, late); err != nil {
					t.Fatal(err)
				}
			}
			var group []*viewPane
			for _, p := range c.panes {
				vp := &viewPane{windowID: p.name, harness: "codex", tr: tr, exact: p.exact, due: true,
					id: agentIdentity{cwd: cwd, started: t0.Add(p.started)}}
				if p.joined != "" {
					vp.joined = path(p.joined)
				}
				group = append(group, vp)
			}
			taken := map[string]bool{}
			for _, n := range c.taken {
				taken[path(n)] = true
			}
			owner := viewCandidates(group, cwd, func(p string) bool { return taken[p] })
			got := map[string]string{}
			for p, f := range owner {
				if p.exact {
					continue
				}
				got[p.windowID] = f
			}
			for _, p := range group {
				want := ""
				if n, ok := c.want[p.windowID]; ok && !p.exact {
					want = path(n)
				}
				if got[p.windowID] != want {
					t.Errorf("pane %s got %q, want %q", p.windowID, got[p.windowID], want)
				}
			}
		})
	}
}
