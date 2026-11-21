package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var internalEnvVars = map[string]string{
	"TUIOS_BASH_LOGIN":           "tuios sets it for its own bash rc file",
	"TUIOS_ZSH_ZDOTDIR":          "tuios sets it for its own zsh shim",
	"TUIOS_HANDOFF_FALLBACK":     "the live upgrade sets it on the image it starts",
	"TUIOS_HANDOFF_KEPT":         "the live upgrade sets it on the image it starts",
	"TUIOS_HANDOFF_OLD_COPY":     "the live upgrade sets it on the image it starts",
	"TUIOS_PLUGIN_GODEBUG":       "the herdr plugin reaper sets it for the command it runs",
	"TUIOS_SOUND_PATH":           "the Windows sound player sets it for PowerShell",
	"TUIOS_TEST_ISOLATED":        "tests set it",
	"TUIOS_TEST_KNOBS":           "tests set it",
	"TUIOS_LOAD1":                "tests and screenshots set it",
	"TUIOS_VIEW_ONLY":            "tests set it",
	"TUIOS_TML_LINK_LEGACY":      "tests set it",
	"TUIOS_GUI_BRIDGE_QUEUE_MAX": "tests set it",
	"TUIOS_LOOK_GALLERY":         "tests set it",
	"TUIOS_RELEASE_API":          "tests set it",
	"TUIOS_GUI_BRIDGE_PPROF":     "a developer sets it to profile the GUI bridge",
	"TUIOS_NO_FASTBOX":           "a developer sets it to compare two render paths",
	"TUIOS_NO_FASTPATH":          "a developer sets it to compare two render paths",
	"TUIOS_NO_PRESHAPED":         "a developer sets it to compare two render paths",
	"TUIOS_RAIL_CHECK":           "a developer sets it to find a stale rail cache",
	"TUIOS_INTEGRATION_ID":       "a marker in the files tuios integration install writes",
	"TUIOS_PANE":                 "a prefix that plugin commands match, not a variable",
}

var exampleEnvVars = map[string]bool{
	"TUIOS_HOOK_TOKEN": true,
}

var envVarLiteral = regexp.MustCompile(`"(TUIOS_[A-Z0-9_]*[A-Z0-9])["=]`)

var envVarName = regexp.MustCompile(`\bTUIOS_[A-Z0-9_]*[A-Z0-9]\b`)

// TestEveryEnvVarIsDocumented fails on a TUIOS_ variable that tuios reads or
// sets and that no file under docs/ names, unless internalEnvVars lists it
// with a reason. It also fails on a variable that a doc names and no file of
// the repo outside docs/ has: a variable that was renamed or removed.
func TestEveryEnvVarIsDocumented(t *testing.T) {
	root := filepath.Join("..", "..")
	documented := map[string]string{}
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil || len(docs) == 0 {
		t.Fatalf("no docs found: %v", err)
	}
	for _, f := range docs {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range envVarName.FindAllString(string(data), -1) {
			documented[v] = filepath.Base(f)
		}
	}

	inCode := map[string]string{}
	var source strings.Builder
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch rel {
			case ".git", ".dev", "docs", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".sh", ".yml", ".yaml", ".nix", ".toml", ".js", ".ts", ".py", ".lua", ".fish", ".zsh", ".bash", "":
		default:
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 1<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		source.Write(data)
		source.WriteByte('\n')
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && !strings.HasPrefix(rel, "e2e"+string(filepath.Separator)) {
			for _, m := range envVarLiteral.FindAllStringSubmatch(string(data), -1) {
				inCode[m[1]] = rel
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inCode) < 50 {
		t.Fatalf("found %d TUIOS_ variables in the code, want all of them", len(inCode))
	}

	for _, v := range slices.Sorted(mapKeys(inCode)) {
		_, doc := documented[v]
		reason, internal := internalEnvVars[v]
		switch {
		case !doc && !internal:
			t.Errorf("%s uses %s, and no doc names it. Document it, or add it to internalEnvVars with a reason", inCode[v], v)
		case doc && internal:
			t.Errorf("%s names %s, and internalEnvVars says no doc does (%s). Take it out of internalEnvVars", documented[v], v, reason)
		}
	}
	for v := range internalEnvVars {
		if _, ok := inCode[v]; !ok {
			t.Errorf("internalEnvVars lists %s, which the code no longer uses", v)
		}
	}
	for v := range exampleEnvVars {
		if _, ok := documented[v]; !ok {
			t.Errorf("exampleEnvVars lists %s, which no doc names", v)
		}
	}
	all := source.String()
	for _, v := range slices.Sorted(mapKeys(documented)) {
		if !exampleEnvVars[v] && !strings.Contains(all, v) {
			t.Errorf("%s names %s, which no file of the repo outside docs/ has", documented[v], v)
		}
	}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
