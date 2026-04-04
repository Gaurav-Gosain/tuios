package app

import (
	"testing"
)

func TestExtractPathAtPosition(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		col      int
		expected string
	}{
		{
			name:     "absolute path",
			line:     "error in /home/user/project/main.go:42",
			col:      20,
			expected: "/home/user/project/main.go:42",
		},
		{
			name:     "relative path",
			line:     "  ./src/utils.ts:10:5",
			col:      10,
			expected: "./src/utils.ts:10:5",
		},
		{
			name:     "home path",
			line:     "config at ~/config/settings.toml",
			col:      18,
			expected: "~/config/settings.toml",
		},
		{
			name:     "no path",
			line:     "hello world",
			col:      5,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractPathAtPosition(tt.line, tt.col)
			if result != tt.expected {
				t.Errorf("ExtractPathAtPosition(%q, %d) = %q, want %q", tt.line, tt.col, result, tt.expected)
			}
		})
	}
}

func TestResolvePathWithCWD(t *testing.T) {
	tests := []struct {
		name string
		path string
		cwd  string
		want bool // whether result should be non-empty (file exists)
	}{
		{
			name: "empty path",
			path: "",
			cwd:  "/tmp",
			want: false,
		},
		{
			name: "nonexistent file",
			path: "/nonexistent/file.go",
			cwd:  "/tmp",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolvePathWithCWD(tt.path, tt.cwd)
			if (result != "") != tt.want {
				t.Errorf("ResolvePathWithCWD(%q, %q) = %q, want empty=%v", tt.path, tt.cwd, result, !tt.want)
			}
		})
	}
}
