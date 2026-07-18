package scrollback

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// JSONBlock represents a JSON object or array found in command output.
type JSONBlock struct {
	Name   string // description (e.g., "object" or "array")
	Pretty string // pretty-printed JSON
	Raw    string // raw JSON string
}

// PathBlock represents a file path or URL found in command output.
type PathBlock struct {
	Raw   string
	Path  string
	Line  int // extracted line number, 0 if none
	Col   int // extracted column number, 0 if none
	IsURL bool
}

// ExtractJSON finds valid JSON objects and arrays in the output text using bracket matching.
func ExtractJSON(output string) []JSONBlock {
	var blocks []JSONBlock
	runes := []rune(output)

	for i := 0; i < len(runes); i++ {
		var open, close rune
		switch runes[i] {
		case '{':
			open, close = '{', '}'
		case '[':
			open, close = '[', ']'
		default:
			continue
		}

		// Bracket-matching scan
		depth := 0
		inStr := false
		escaped := false
		end := -1
		for j := i; j < len(runes); j++ {
			ch := runes[j]
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && inStr {
				escaped = true
				continue
			}
			if ch == '"' {
				inStr = !inStr
				continue
			}
			if inStr {
				continue
			}
			if ch == open {
				depth++
			} else if ch == close {
				depth--
				if depth == 0 {
					end = j + 1
					break
				}
			}
		}
		if end <= i {
			continue
		}

		raw := string(runes[i:end])
		if !json.Valid([]byte(raw)) {
			continue
		}

		var pretty bytes.Buffer
		if err := json.Indent(&pretty, []byte(raw), "", "  "); err != nil {
			continue
		}

		name := "object"
		if runes[i] == '[' {
			name = "array"
		}

		blocks = append(blocks, JSONBlock{
			Name:   name,
			Pretty: pretty.String(),
			Raw:    raw,
		})

		i = end - 1 // skip past matched block
	}

	return blocks
}

// pathRegex matches filesystem paths and URLs.
var pathRegex = regexp.MustCompile(
	`(?:` +
		// URLs
		`https?://[^\s"'<>]+|` +
		// Absolute paths with optional :line:col
		`/[^\s"'<>:]+(?::\d+(?::\d+)?)?|` +
		// Relative paths with optional :line:col
		`\.{1,2}/[^\s"'<>:]+(?::\d+(?::\d+)?)?` +
		`)`,
)

// lineColRegex extracts :line and optional :col from a path suffix.
var lineColRegex = regexp.MustCompile(`:(\d+)(?::(\d+))?$`)

// ExtractPaths finds file paths and URLs in the output text.
func ExtractPaths(output string) []PathBlock {
	matches := pathRegex.FindAllString(output, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var blocks []PathBlock

	for _, raw := range matches {
		if seen[raw] {
			continue
		}
		seen[raw] = true

		isURL := strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")

		path := raw
		line, col := 0, 0

		if !isURL {
			if loc := lineColRegex.FindStringSubmatchIndex(raw); loc != nil {
				path = raw[:loc[0]]
				if loc[2] >= 0 {
					line = parseInt(raw[loc[2]:loc[3]])
				}
				if loc[4] >= 0 {
					col = parseInt(raw[loc[4]:loc[5]])
				}
			}
		}

		blocks = append(blocks, PathBlock{
			Raw:   raw,
			Path:  path,
			Line:  line,
			Col:   col,
			IsURL: isURL,
		})
	}

	return blocks
}

func parseInt(s string) int {
	n := 0
	for _, b := range s {
		if b >= '0' && b <= '9' {
			n = n*10 + int(b-'0')
		}
	}
	return n
}
