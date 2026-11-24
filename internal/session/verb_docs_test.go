package session

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/tarpack"
)

// verbDocGroups names the .verbs.json file that holds the docs of each group
// of verbs. A verb in no group here is registered in verb_protocol.go, and
// its doc is in verb_protocol.verbs.json.
func verbDocGroups() []struct {
	file  string
	verbs map[string]verbEntry
} {
	return []struct {
		file  string
		verbs map[string]verbEntry
	}{
		{"drop_files.verbs.json", dropVerbs()},
		{"pane_record.verbs.json", recordVerbs()},
		{"task_verify.verbs.json", taskVerifyVerbs()},
		{"transfer.verbs.json", transferVerbs()},
		{"transfer_speed.verbs.json", speedVerbs()},
		{"transfer_tree.verbs.json", treeVerbs()},
		{"verb_buffers.verbs.json", bufferVerbs()},
		{"verb_exec.verbs.json", execVerbs()},
		{"verb_ship.verbs.json", shipVerbs()},
		{"verb_checkpoint.verbs.json", checkpointVerbs()},
		{"verb_preview.verbs.json", previewVerbs()},
		{"verb_preview_run.verbs.json", previewRunVerbs()},
		{"verb_files.verbs.json", fileVerbs()},
		{"verb_find.verbs.json", findVerbs()},
		{"verb_protocol_task.verbs.json", taskVerbs()},
		{"verb_protocol_agents.verbs.json", agentWorkVerbs()},
		{"verb_receipts.verbs.json", receiptVerbs()},
		{"verb_task_dispatch.verbs.json", taskDispatchVerbs(verbParam{})},
		{"handoff_testhooks.verbs.json", handoffTestVerbs()},
	}
}

// TestVerbDocsArchiveMatchesFiles holds verbdocs.tar.gz to the .verbs.json
// files. The tests read the files and the binary reads the archive, so a doc
// edited without go generate would pass here and ship the old text.
func TestVerbDocsArchiveMatchesFiles(t *testing.T) {
	packed, err := tarpack.Unpack(verbDocsArchive)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*.verbs.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != len(files) {
		t.Errorf("the archive holds %d files and the folder %d; run go generate ./internal/session", len(packed), len(files))
	}
	for _, name := range files {
		disk, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(packed[name], disk) {
			t.Errorf("%s differs from its copy in the archive; run go generate ./internal/session", name)
		}
	}
}

// TestVerbDocsMatchTheRegistry holds each .verbs.json file to the verbs the Go
// file beside it registers: every verb has a doc in that file, every doc there
// names one of its verbs, and the doc names the same parameters and result
// fields as the registry, in the same order.
func TestVerbDocsMatchTheRegistry(t *testing.T) {
	want := map[string]map[string]verbEntry{}
	grouped := map[string]bool{}
	for _, g := range verbDocGroups() {
		verbs := map[string]verbEntry{}
		for name, entry := range g.verbs {
			// The registry's entry, where there is one: a group may take a
			// parameter from its caller, as taskDispatchVerbs does.
			if reg, ok := verbRegistry()[name]; ok {
				entry = reg
			}
			verbs[name] = entry
			grouped[name] = true
		}
		want[g.file] = verbs
	}
	rest := map[string]verbEntry{}
	for name, entry := range verbRegistry() {
		if !grouped[name] {
			rest[name] = entry
		}
	}
	want["verb_protocol.verbs.json"] = rest

	files, err := filepath.Glob("*.verbs.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if _, ok := want[file]; !ok {
			t.Errorf("%s holds the docs of no group of verbs; add its group to verbDocGroups or remove the file", file)
		}
	}
	for _, file := range slices.Sorted(maps.Keys(want)) {
		verbs := want[file]
		data, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		var docs map[string]verbDocText
		if err := jsonx.Unmarshal(data, &docs); err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(verbs)) {
			doc, ok := docs[name]
			if !ok {
				t.Errorf("verb %s has no doc in %s", name, file)
				continue
			}
			checkFieldNames(t, file, name, "parameter", verbs[name].params, doc.Params)
			checkFieldNames(t, file, name, "result field", verbs[name].returns, doc.Returns)
		}
		for _, name := range slices.Sorted(maps.Keys(docs)) {
			if _, ok := verbs[name]; !ok {
				t.Errorf("%s has a doc for %s, which its Go file does not register", file, name)
			}
		}
	}
}

func checkFieldNames(t *testing.T, file, verb, what string, fields []verbParam, text []verbFieldText) {
	t.Helper()
	got := make([]string, len(text))
	for i, f := range text {
		got[i] = f.Name
	}
	want := make([]string, len(fields))
	for i, f := range fields {
		want[i] = f.Name
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: the %s names of %s are %v, and the registry has %v", file, what, verb, got, want)
	}
}

// TestVerbDocsAreDecodedOnlyWhenAsked holds the daemon to decoding the docs
// only for list-verbs: a daemon start and an ordinary verb leave them packed.
func TestVerbDocsAreDecodedOnlyWhenAsked(t *testing.T) {
	if verbDocsDecoded.Load() {
		t.Skip("an earlier test in this binary already read the docs")
	}
	_, socketPath := startTestDaemon(t)
	c := dialVerb(t, socketPath)
	callVerb(t, c, "hello", nil)
	callVerb(t, c, "list-sessions", nil)
	if verbDocsDecoded.Load() {
		t.Fatal("the daemon decoded the verb docs without a list-verbs call")
	}
	callVerb(t, c, "list-verbs", map[string]any{"verb": "hello"})
	if !verbDocsDecoded.Load() {
		t.Fatal("list-verbs did not read the verb docs")
	}
}
