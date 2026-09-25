package input

import (
	"testing"
)

func TestGetCharType(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "empty string",
			content: "",
			want:    0, // whitespace
		},
		{
			name:    "space",
			content: " ",
			want:    0, // whitespace
		},
		{
			name:    "tab",
			content: "\t",
			want:    0, // whitespace
		},
		{
			name:    "letter",
			content: "a",
			want:    1, // word
		},
		{
			name:    "digit",
			content: "5",
			want:    1, // word
		},
		{
			name:    "underscore",
			content: "_",
			want:    1, // word (part of identifiers)
		},
		{
			name:    "punctuation",
			content: ".",
			want:    2, // punctuation
		},
		{
			name:    "bracket",
			content: "(",
			want:    2, // punctuation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getCharType(tt.content)
			if got != tt.want {
				t.Errorf("getCharType(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}
