package omarchy

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestResolveMatchesOmarchy holds the port to the script it ports: for every
// shipped theme, the v3 layout and the quirk cases, Resolve has to print what
// omarchy-theme-color --all printed, key for key.
func TestResolveMatchesOmarchy(t *testing.T) {
	files, err := filepath.Glob("testdata/resolved/*.tsv")
	if err != nil || len(files) < 23 {
		t.Fatalf("expected the resolved fixtures, found %d (%v)", len(files), err)
	}
	for _, golden := range files {
		name := strings.TrimSuffix(filepath.Base(golden), ".tsv")
		dir := filepath.Join("testdata", "themes", name)
		switch {
		case strings.HasPrefix(name, "case-"):
			dir = filepath.Join("testdata", "cases", strings.TrimPrefix(name, "case-"))
		case strings.HasPrefix(name, "v3-"):
			dir = filepath.Join("testdata", "v3", strings.TrimPrefix(name, "v3-"))
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "colors.toml"))
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(filepath.Join(dir, "light.mode"))
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			got := Resolve(data, statErr == nil)
			if out := tsv(got); out != string(want) {
				t.Errorf("Resolve differs from omarchy-theme-color --all\n got:\n%s\nwant:\n%s", out, want)
			}
		})
	}
}

// tsv prints c as omarchy-theme-color --all does.
func tsv(c map[string]string) string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\t" + c[k] + "\n")
	}
	return b.String()
}

// FuzzResolve feeds the parser arbitrary files. Theme files come from
// strangers, so whatever the bytes, Resolve must not panic, every key it
// keeps must pass Omarchy's key check, every value must be one Omarchy would
// keep or one it derives, and the palette built from it must have every role
// set.
func FuzzResolve(f *testing.F) {
	for _, pattern := range []string{"testdata/themes/*/colors.toml", "testdata/cases/*/colors.toml", "testdata/v3/*/colors.toml"} {
		files, _ := filepath.Glob(pattern)
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err == nil {
				f.Add(data, false)
			}
		}
	}
	f.Add([]byte("background = \"#fff\"\nmode = \"light\"\n"), true)
	f.Fuzz(func(t *testing.T, data []byte, lightFile bool) {
		c := Resolve(data, lightFile)
		for k, v := range c {
			if !allIn(k, keyChars) || k == "" {
				t.Fatalf("kept key %q", k)
			}
			if !allIn(v, valueChars) {
				t.Fatalf("kept %s = %q", k, v)
			}
		}
		for _, k := range []string{"mode", "theme_type", "cursor", "background", "foreground", "color0", "color15"} {
			if _, ok := c[k]; !ok {
				t.Fatalf("no %s in %v", k, c)
			}
		}
		if again := Resolve(bytes.Clone(data), lightFile); tsv(again) != tsv(c) {
			t.Fatal("Resolve is not deterministic")
		}
		p := FromColors(c)
		roles := []any{p.Background, p.Foreground, p.Cursor, p.SelectionBackground, p.SelectionForeground,
			p.Surface, p.Muted, p.Accent, p.Border, p.Success, p.Warning, p.Error, p.Info}
		for i, c := range p.ANSI {
			if c == nil {
				t.Fatalf("ANSI %d unset", i)
			}
		}
		for i, r := range roles {
			if r == nil {
				t.Fatalf("role %d unset", i)
			}
		}
	})
}
