package input

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestCellContent(t *testing.T) {
	tests := []struct {
		name string
		cell *uv.Cell
		want string
	}{
		{
			name: "nil cell",
			cell: nil,
			want: "",
		},
		{
			name: "cell with content",
			cell: &uv.Cell{Content: "A"},
			want: "A",
		},
		{
			name: "cell with empty content",
			cell: &uv.Cell{Content: ""},
			want: "",
		},
		{
			name: "cell with space",
			cell: &uv.Cell{Content: " "},
			want: " ",
		},
		{
			name: "cell with unicode",
			cell: &uv.Cell{Content: "世"},
			want: "世",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cellContent(tt.cell)
			if got != tt.want {
				t.Errorf("cellContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

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
