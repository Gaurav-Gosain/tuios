package tape

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// maxSourceDepth bounds how deep Source lines may nest, which also stops a
// tape that includes itself through a chain the cycle check has not seen yet.
const maxSourceDepth = 16

// Script is a tape read from a file, with every Source line replaced by the
// lines of the file it names.
//
// The text is what a tape sent to a running session carries, since the
// session has no way to read the files. Each of its lines remembers where it
// came from, so an error the session reports against the text can be put
// back on the file and line a person wrote.
type Script struct {
	// Text is the tape with its Source lines resolved.
	Text string
	// Commands is Text parsed, each command placed in the file it came from.
	Commands []Command
	// Errors are the parse errors, each placed in the file it came from.
	Errors []string

	origins    []lineOrigin // origins[i] is where line i+1 of Text came from
	sourceErrs []sourceErr
}

// lineOrigin is the file and line one line of Script.Text was read from. file
// is empty for the tape that was loaded.
type lineOrigin struct {
	file string
	line int
}

// LoadFile reads the tape at path and resolves its Source lines. A Source
// path is relative to the directory of the tape that names it. A file that
// cannot be read, a cycle and nesting past maxSourceDepth are reported as
// errors at the Source line, like any other mistake in the tape.
func LoadFile(path string) (*Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &Script{}
	var text strings.Builder
	abs, _ := filepath.Abs(path)
	s.inline(&text, string(data), "", filepath.Dir(path), []string{abs})
	s.Text = text.String()

	commands, errs := ParseFile(s.Text)
	for i := range commands {
		if o, ok := s.origin(commands[i].Line); ok {
			commands[i].Line, commands[i].File = o.line, o.file
		}
	}
	s.Commands = commands
	for _, e := range s.sourceErrs {
		errs = append(errs, fmt.Sprintf("line %d, column 1: %s", e.line, e.msg))
	}
	for _, e := range errs {
		s.Errors = append(s.Errors, s.Locate(e))
	}
	return s, nil
}

// inline appends content to text line by line, replacing each Source line
// with the lines of the file it names. file is the name the errors give
// content ("" for the loaded tape), dir is where its Source paths are
// relative to, and stack holds the files being read, outermost first.
func (s *Script) inline(text *strings.Builder, content, file, dir string, stack []string) {
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, line := range lines {
		target, isSource := sourceTarget(line)
		if !isSource {
			s.add(text, line, file, i+1)
			continue
		}
		fail := func(msg string) {
			// The line stays as it is, so the error points at it. A tape with
			// an error does not run, so the Source is never executed.
			s.add(text, line, file, i+1)
			s.sourceErrs = append(s.sourceErrs, sourceErr{line: len(s.origins), msg: msg})
		}
		full := target
		if !filepath.IsAbs(full) {
			full = filepath.Join(dir, full)
		}
		abs, _ := filepath.Abs(full)
		switch {
		case len(stack) >= maxSourceDepth:
			fail(fmt.Sprintf("Source nests deeper than %d files", maxSourceDepth))
			continue
		case slices.Contains(stack, abs):
			fail(fmt.Sprintf("%s is already being read, so this Source would never end", target))
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			fail(fmt.Sprintf("cannot read %s: %v", target, err))
			continue
		}
		name := target
		if file != "" {
			name = filepath.Join(filepath.Dir(file), target)
		}
		s.inline(text, string(data), name, filepath.Dir(full), append(stack, abs))
	}
}

// sourceErr is a Source line that could not be resolved. The parser accepts
// the line it is left as, so the error is added after parsing.
type sourceErr struct {
	line int
	msg  string
}

// add appends one line of text and remembers where it came from.
func (s *Script) add(text *strings.Builder, line, file string, n int) {
	text.WriteString(line)
	text.WriteByte('\n')
	s.origins = append(s.origins, lineOrigin{file: file, line: n})
}

// sourceTarget reports whether line is a Source line, and the path it names.
func sourceTarget(line string) (string, bool) {
	commands, errs := ParseFile(line)
	if len(errs) > 0 || len(commands) != 1 || commands[0].Type != CommandTypeSource || len(commands[0].Args) != 1 {
		return "", false
	}
	return commands[0].Args[0], true
}

func (s *Script) origin(line int) (lineOrigin, bool) {
	if line < 1 || line > len(s.origins) {
		return lineOrigin{}, false
	}
	return s.origins[line-1], true
}

// linePrefix matches the place an error message starts with.
var linePrefix = lazyre.New(`^line (\d+), column (\d+)`)

// Locate rewrites the "line N, column C" an error message starts with, which
// counts lines of Text, to the file and line the line came from. A message
// with no such place is returned unchanged.
func (s *Script) Locate(msg string) string {
	m := linePrefix().FindStringSubmatchIndex(msg)
	if m == nil {
		return msg
	}
	n, _ := strconv.Atoi(msg[m[2]:m[3]])
	o, ok := s.origin(n)
	if !ok {
		return msg
	}
	where := fmt.Sprintf("line %d, column %s", o.line, msg[m[4]:m[5]])
	if o.file != "" {
		where = o.file + " " + where
	}
	return where + msg[m[1]:]
}
