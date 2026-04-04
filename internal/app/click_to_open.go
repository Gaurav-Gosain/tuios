package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// pathPattern matches common file path patterns in terminal output.
// Matches: /absolute/path, ./relative/path, ~/home/path, path/to/file.ext
// Optionally followed by :line or :line:col
var pathPattern = regexp.MustCompile(`(?:(?:[~./]|[a-zA-Z]:)?(?:/[\w.@-]+)+(?:\.\w+)?(?::\d+(?::\d+)?)?)`)

// ExtractPathAtPosition scans the given line for a file path near the column position.
// Returns the path (with optional :line:col suffix) or empty string if none found.
func ExtractPathAtPosition(line string, col int) string {
	matches := pathPattern.FindAllStringIndex(line, -1)
	if len(matches) == 0 {
		return ""
	}

	// Find the match that contains or is nearest to the click position
	for _, m := range matches {
		start, end := m[0], m[1]
		if col >= start && col <= end {
			return line[start:end]
		}
	}

	// If no exact match, find nearest
	bestMatch := ""
	bestDist := len(line)
	for _, m := range matches {
		start, end := m[0], m[1]
		mid := (start + end) / 2
		dist := col - mid
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestMatch = line[start:end]
		}
	}

	// Only use nearest match if it's within 5 characters
	if bestDist <= 5 {
		return bestMatch
	}
	return ""
}

// ResolvePathWithCWD resolves a potentially relative path against the given CWD.
// Expands ~ to home directory. Strips :line:col suffix for existence checking.
func ResolvePathWithCWD(path, cwd string) string {
	if path == "" {
		return ""
	}

	// Strip :line:col suffix for file existence check
	cleanPath := path
	if idx := strings.Index(cleanPath, ":"); idx > 0 {
		cleanPath = cleanPath[:idx]
	}

	// Expand ~
	if strings.HasPrefix(cleanPath, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			cleanPath = filepath.Join(home, cleanPath[2:])
		}
	}

	// Make absolute using CWD
	if !filepath.IsAbs(cleanPath) && cwd != "" {
		cleanPath = filepath.Join(cwd, cleanPath)
	}

	// Check if the resolved path exists
	if _, err := os.Stat(cleanPath); err != nil {
		return ""
	}

	// Return the original path (with :line:col) but with resolved base
	if strings.Contains(path, ":") {
		suffix := path[strings.Index(path, ":"):]
		return cleanPath + suffix
	}
	return cleanPath
}

// OpenInEditor opens a file path in the user's $EDITOR.
// Supports path:line:col format for editors that support it (vim, nvim, code, etc.).
func OpenInEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	// Parse path:line:col
	parts := strings.SplitN(path, ":", 3)
	filePath := parts[0]

	var args []string
	switch {
	case strings.Contains(editor, "vim") || strings.Contains(editor, "nvim"):
		args = append(args, filePath)
		if len(parts) > 1 {
			args = append(args, "+"+parts[1])
		}
	case strings.Contains(editor, "code"):
		if len(parts) > 1 {
			args = append(args, "--goto", path) // VS Code supports path:line:col
		} else {
			args = append(args, filePath)
		}
	default:
		args = append(args, filePath)
	}

	// #nosec G204 - editor is user-controlled via $EDITOR env var
	cmd := exec.Command(editor, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}
