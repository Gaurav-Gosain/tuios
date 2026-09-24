package review

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRawNumstat reads the output of git diff --raw --numstat -z: every
// changed file with its status, then every file's added and removed counts,
// in the same order. A binary file's counts are "-". A rename carries both
// paths. The files come back in git's order, with counts filled in.
func ParseRawNumstat(out string) ([]File, error) {
	tokens := strings.Split(out, "\x00")
	var files []File
	byPath := map[string]int{}
	counted := 0
	for i := 0; i < len(tokens); i++ {
		tok := strings.TrimLeft(tokens[i], "\n")
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, ":") {
			// :oldmode newmode oldsha newsha STATUS, then the path, or for a
			// rename or copy the old path and the new.
			fields := strings.Fields(tok)
			if len(fields) < 5 || i+1 >= len(tokens) {
				return nil, fmt.Errorf("unexpected raw diff line %q", tok)
			}
			letter := fields[4][:1]
			f := File{}
			switch letter {
			case "R", "C":
				if i+2 >= len(tokens) {
					return nil, fmt.Errorf("unexpected raw diff line %q", tok)
				}
				f.OldPath, f.Path = tokens[i+1], tokens[i+2]
				i += 2
				if letter == "C" {
					// A copy is a new file whose content came from another.
					f.Status, f.OldPath = StatusAdded, ""
				} else {
					f.Status = StatusRenamed
				}
			default:
				f.Path = tokens[i+1]
				i++
				switch letter {
				case "A":
					f.Status = StatusAdded
				case "D":
					f.Status = StatusDeleted
				default:
					// M, T (a type change) and anything newer read as a
					// modification.
					f.Status = StatusModified
				}
			}
			byPath[f.Path] = len(files)
			files = append(files, f)
			continue
		}
		// added TAB removed TAB path, or for a rename added TAB removed TAB
		// with the two paths in the next tokens.
		parts := strings.SplitN(tok, "\t", 3)
		if len(parts) < 3 {
			return nil, fmt.Errorf("unexpected numstat line %q", tok)
		}
		path := parts[2]
		if path == "" {
			if i+2 >= len(tokens) {
				return nil, fmt.Errorf("unexpected numstat line %q", tok)
			}
			path = tokens[i+2]
			i += 2
		}
		idx, ok := byPath[path]
		if !ok {
			// numstat without a raw line: take it in order.
			if counted < len(files) {
				idx = counted
			} else {
				continue
			}
		}
		counted++
		f := &files[idx]
		if parts[0] == "-" && parts[1] == "-" {
			f.Binary = true
			continue
		}
		f.Added, _ = strconv.Atoi(parts[0])
		f.Removed, _ = strconv.Atoi(parts[1])
	}
	return files, nil
}

// ParseHunkHeader reads "@@ -a,b +c,d @@ ..." into its four numbers. A
// missing count is 1, as in git's output.
func ParseHunkHeader(header string) (oldStart, oldLines, newStart, newLines int, err error) {
	rest, ok := strings.CutPrefix(header, "@@ -")
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("not a hunk header: %q", header)
	}
	end := strings.Index(rest, " @@")
	if end < 0 {
		return 0, 0, 0, 0, fmt.Errorf("not a hunk header: %q", header)
	}
	oldPart, newPart, ok := strings.Cut(rest[:end], " +")
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("not a hunk header: %q", header)
	}
	if oldStart, oldLines, err = parseRange(oldPart); err != nil {
		return 0, 0, 0, 0, err
	}
	if newStart, newLines, err = parseRange(newPart); err != nil {
		return 0, 0, 0, 0, err
	}
	return oldStart, oldLines, newStart, newLines, nil
}

func parseRange(s string) (int, int, error) {
	startText, countText, has := strings.Cut(s, ",")
	start, err := strconv.Atoi(startText)
	if err != nil || start < 0 {
		return 0, 0, fmt.Errorf("bad hunk range %q", s)
	}
	count := 1
	if has {
		if count, err = strconv.Atoi(countText); err != nil || count < 0 {
			return 0, 0, fmt.Errorf("bad hunk range %q", s)
		}
	}
	return start, count, nil
}
