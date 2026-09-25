package main

import (
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
