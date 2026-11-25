// Package previewspec checks a quick look previewer and fills in its
// command. A previewer maps files to a command whose output quick look
// shows: [[quick_look.previewer]] in config.toml, or a plugin's
// [[previewer]]. Both are checked here, so the two cannot disagree on what
// a valid previewer is.
package previewspec

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Outputs a previewer may declare. The empty output is ansi.
const (
	OutputANSI  = "ansi"
	OutputText  = "text"
	OutputImage = "image"
)

// TimeoutDefault and TimeoutMax bound one run of a previewer.
const (
	TimeoutDefault = 5 * time.Second
	TimeoutMax     = 30 * time.Second
)

// Size is what the placeholders of a command are filled with.
type Size struct {
	Width, Height, Line int
}

// Spec is the keys a previewer has both in config.toml and in a plugin.
type Spec struct {
	// Match is the name patterns. A pattern with a / matches the whole path.
	Match []string `json:"match,omitempty" toml:"match"`
	// Mime is the MIME type patterns, sniffed from the start of the file.
	Mime []string `json:"mime,omitempty" toml:"mime"`
	// Folder says the previewer is for folders.
	Folder  bool     `json:"folder,omitempty" toml:"folder"`
	Command []string `json:"command" toml:"command"`
	// Output is ansi (the default), text or image.
	Output  string `json:"output,omitempty" toml:"output"`
	Timeout string `json:"timeout,omitempty" toml:"timeout"`
}

// placeholders are the names a command may hold in braces.
var placeholders = [...]string{"path", "name", "dir", "width", "height", "line"}

// Check returns what is wrong with a previewer, in a sentence, or "".
func (s Spec) Check(name string) string {
	if strings.TrimSpace(name) == "" {
		return "A previewer needs a name."
	}
	why := ""
	switch command, output := s.Command, s.Output; {
	case len(command) == 0 || strings.TrimSpace(command[0]) == "":
		why = "needs a command."
	case len(s.Match) == 0 && len(s.Mime) == 0 && !s.Folder:
		why = "needs match, mime or folder."
	case output != "" && output != OutputANSI && output != OutputText && output != OutputImage:
		why = "needs an output of ansi, text or image."
	}
	for _, g := range append(append([]string(nil), s.Match...), s.Mime...) {
		if _, err := path.Match(g, ""); err != nil && why == "" {
			why = "has a bad pattern: " + g + "."
		}
	}
	for _, arg := range s.Command {
		for rest := arg; ; {
			i := strings.IndexByte(rest, '{')
			if i < 0 {
				break
			}
			j := strings.IndexByte(rest[i:], '}')
			if j < 0 {
				break
			}
			// Only a lowercase word is read as a placeholder, so a command
			// can hold {} or a jq filter such as {a: .b}.
			if key := rest[i+1 : i+j]; placeholderWord(key) && !slices.Contains(placeholders[:], key) {
				if why == "" {
					why = "names {" + key + "}. Use {path}, {name}, {dir}, {width}, {height} or {line}."
				}
			}
			rest = rest[i+j+1:]
		}
	}
	if _, ok := Timeout(s.Timeout); !ok && why == "" {
		why = "needs a timeout from 1s to 30s, such as 5s."
	}
	if why == "" {
		return ""
	}
	return "The previewer " + name + " " + why
}

// placeholderWord reports whether key is a lowercase word of two letters
// or more, as a placeholder is.
func placeholderWord(key string) bool {
	for _, c := range key {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return len(key) > 1
}

// Timeout reads a previewer's timeout. The empty timeout is TimeoutDefault.
func Timeout(s string) (time.Duration, bool) {
	if s == "" {
		return TimeoutDefault, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > TimeoutMax {
		return 0, false
	}
	return d, true
}

// Expand fills the placeholders of command for the file at file. Each
// argument stays one argument, whatever the path holds, and no shell reads
// it.
func Expand(command []string, file string, dir bool, size Size) []string {
	folder := path.Dir(file)
	if dir {
		folder = file
	}
	name := path.Base(file)
	if strings.HasPrefix(name, "-") {
		// A name is never read as a flag.
		name = "./" + name
	}
	r := strings.NewReplacer("{path}", file, "{name}", name, "{dir}", folder,
		"{width}", strconv.Itoa(size.Width), "{height}", strconv.Itoa(size.Height), "{line}", strconv.Itoa(max(size.Line, 1)))
	out := make([]string, len(command))
	for i, arg := range command {
		out[i] = r.Replace(arg)
	}
	return out
}

// UsesSize reports whether command names {width} or {height}, so a resize
// runs it again.
func UsesSize(command []string) bool {
	for _, arg := range command {
		if strings.Contains(arg, "{width}") || strings.Contains(arg, "{height}") {
			return true
		}
	}
	return false
}

// Matches reports whether a previewer matches the file at file. A pattern
// with a / matches the whole path, others the name. mime is asked only when
// a mime pattern needs it.
func (s Spec) Matches(file string, dir bool, mime func() string) bool {
	if dir {
		return s.Folder
	}
	name := path.Base(file)
	for _, g := range s.Match {
		target := name
		if strings.Contains(g, "/") {
			target = file
		}
		if ok, _ := path.Match(g, target); ok {
			return true
		}
	}
	if len(s.Mime) == 0 || mime == nil {
		return false
	}
	m, _, _ := strings.Cut(mime(), ";")
	for _, g := range s.Mime {
		if ok, _ := path.Match(g, strings.TrimSpace(m)); ok {
			return true
		}
	}
	return false
}
