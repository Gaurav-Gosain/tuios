package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The three multi copy formats, held to exact output, and the untrusted pane
// title held to not being able to change the shape of any of them.

var multiCopyTestPanes = []MultiCopyPane{
	{Index: 0, WindowID: "w-a", Title: "root@node-01", Lines: []string{"headnode\t0002c6a6\t20260522T154557Z"}},
	{Index: 3, WindowID: "w-b", Title: "", Lines: []string{"ip  0  IP", "icmp  1  ICMP"}},
}

func TestFormatMultiCopyPlain(t *testing.T) {
	got := FormatMultiCopy(config.MultiCopyFormatPlain, multiCopyTestPanes)
	want := "headnode\t0002c6a6\t20260522T154557Z\nip  0  IP\nicmp  1  ICMP\n"
	if got != want {
		t.Errorf("plain =\n%q\nwant\n%q", got, want)
	}
	// An unknown format is plain, never an empty clipboard.
	if FormatMultiCopy("yaml", multiCopyTestPanes) != want {
		t.Error("an unknown format did not fall back to plain")
	}
}

func TestFormatMultiCopyMarkdown(t *testing.T) {
	got := FormatMultiCopy(config.MultiCopyFormatMarkdown, multiCopyTestPanes)
	want := "## Pane 0: root@node-01\n\n```\nheadnode\t0002c6a6\t20260522T154557Z\n```\n\n" +
		"## Pane 3\n\n```\nip  0  IP\nicmp  1  ICMP\n```\n"
	if got != want {
		t.Errorf("markdown =\n%s\nwant\n%s", got, want)
	}
}

// A pane's own output can hold a fence. The block around it has to be longer.
func TestFormatMultiCopyMarkdownFenceOutlastsTheContent(t *testing.T) {
	got := FormatMultiCopy(config.MultiCopyFormatMarkdown, []MultiCopyPane{
		{Index: 1, Lines: []string{"```", "rm -rf /", "````"}},
	})
	if !strings.Contains(got, "\n`````\n```\nrm -rf /\n````\n`````\n") {
		t.Errorf("the fence is not longer than the content's backtick runs:\n%s", got)
	}
}

func TestFormatMultiCopyJSON(t *testing.T) {
	got := FormatMultiCopy(config.MultiCopyFormatJSON, multiCopyTestPanes)
	var back []map[string]any
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("json is not JSON: %v\n%s", err, got)
	}
	if len(back) != 2 {
		t.Fatalf("json has %d panes, want 2", len(back))
	}
	for _, k := range []string{"pane", "window_id", "title", "lines"} {
		if _, ok := back[0][k]; !ok {
			t.Errorf("json pane has no %q key: %s", k, got)
		}
	}
	if back[1]["pane"].(float64) != 3 || back[1]["window_id"] != "w-b" {
		t.Errorf("json second pane = %v", back[1])
	}
	lines := back[0]["lines"].([]any)
	if len(lines) != 1 || lines[0] != "headnode\t0002c6a6\t20260522T154557Z" {
		t.Errorf("json lines = %v", lines)
	}
	// "<" is text here, not HTML.
	lt := FormatMultiCopy(config.MultiCopyFormatJSON, []MultiCopyPane{{Lines: []string{"a<b>&c"}}})
	if !strings.Contains(lt, `"a<b>&c"`) {
		t.Errorf("json escaped HTML characters: %s", lt)
	}
	// No panes is an empty array, not null.
	if strings.TrimSpace(FormatMultiCopy(config.MultiCopyFormatJSON, nil)) != "[]" {
		t.Error("json with no panes is not []")
	}
}

// A remote shell sets the title. Nothing in it may break out of the header or
// the string it is printed in, or hide what it says.
func TestFormatMultiCopyUntrustedTitles(t *testing.T) {
	evil := "node-07\n\n```\n# injected\x1b]52;c;ZXZpbA==\x07 ‮gnp.exe​⁦x⁩\x00\"quoted\"\\"
	panes := []MultiCopyPane{{Index: 7, WindowID: "w-e", Title: evil, Lines: []string{"ok"}}}

	md := FormatMultiCopy(config.MultiCopyFormatMarkdown, panes)
	header, rest, _ := strings.Cut(md, "\n")
	if !strings.HasPrefix(header, "## Pane 7: node-07") {
		t.Errorf("header = %q", header)
	}
	if rest != "\n```\nok\n```\n" {
		t.Errorf("the title changed the document after its header:\n%q", md)
	}
	for _, bad := range []string{"\x1b", "\x07", "\x00", "‮", "​", "⁦", "⁩"} {
		if strings.Contains(md, bad) {
			t.Errorf("markdown keeps %q from the title", bad)
		}
	}

	js := FormatMultiCopy(config.MultiCopyFormatJSON, panes)
	var back []struct {
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(js), &back); err != nil {
		t.Fatalf("a hostile title broke the JSON: %v\n%s", err, js)
	}
	if len(back) != 1 || len(back[0].Lines) != 1 || back[0].Lines[0] != "ok" {
		t.Fatalf("a hostile title changed the JSON's shape: %s", js)
	}
	title := back[0].Title
	if strings.ContainsAny(title, "\n\x1b\x07\x00‮​⁦⁩") {
		t.Errorf("json title keeps control or invisible characters: %q", title)
	}
	if !strings.Contains(title, `"quoted"\`) || !strings.HasPrefix(title, "node-07") {
		t.Errorf("json title lost its visible text: %q", title)
	}

	long := strings.Repeat("x", 5000)
	if n := len([]rune(sanitizeMultiCopyTitle(long))); n != multiCopyTitleMax {
		t.Errorf("a 5000 character title kept %d characters, want %d", n, multiCopyTitleMax)
	}
}

func TestNextMultiCopyFormatCycles(t *testing.T) {
	f := config.MultiCopyFormatPlain
	var seen []string
	for range 4 {
		f = nextMultiCopyFormat(f)
		seen = append(seen, f)
	}
	if strings.Join(seen, ",") != "markdown,json,plain,markdown" {
		t.Errorf("format cycle = %v", seen)
	}
}
