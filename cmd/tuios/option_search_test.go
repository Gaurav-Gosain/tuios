package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testOptions() []optionRow {
	return []optionRow{
		{Path: "appearance.gap", Type: "int", Default: "0", Description: "Cells of ground between tiled panes"},
		{Path: "appearance.pane_background", Type: "string", Default: "off", Description: "Behind pane content"},
		{Path: "appearance.border_style", Type: "string", Default: "rounded", SessionVal: "double", Description: "Characters a border is drawn with"},
		{Path: "appearance.word_characters", Type: "string", Description: "Punctuation that counts as part of a word"},
	}
}

func TestRankOptions(t *testing.T) {
	opts := testOptions()
	tests := []struct {
		query, want string
	}{
		{"pane bg", "appearance.pane_background"},
		{"pane background", "appearance.pane_background"},
		{"double", "appearance.border_style"},
		{"punctuation", "appearance.word_characters"},
	}
	for _, tt := range tests {
		hits := rankOptions(opts, tt.query)
		if len(hits) == 0 || opts[hits[0].index].Path != tt.want {
			t.Errorf("%q ranked %v first, want %s", tt.query, hits, tt.want)
		}
	}
	if hits := rankOptions(opts, "zqj"); len(hits) != 0 {
		t.Errorf("zqj matched %d options", len(hits))
	}
}

func TestPrintOptionSearch(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"options": testOptions(), "total": 4})
	var buf bytes.Buffer
	if err := printOptionSearch(&buf, raw, "pane bg", false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "appearance.pane_background") {
		t.Errorf("the best match is not first:\n%s", buf.String())
	}

	buf.Reset()
	if err := printOptionSearch(&buf, raw, "nothingmatches", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No options match") {
		t.Errorf("an empty search says %q", buf.String())
	}

	buf.Reset()
	if err := printOptionSearch(&buf, raw, "double", true); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Options []optionRow `json:"options"`
		Total   int         `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, buf.String())
	}
	if out.Total != len(out.Options) || len(out.Options) == 0 || out.Options[0].Path != "appearance.border_style" {
		t.Errorf("the JSON search result is %+v", out)
	}
}
