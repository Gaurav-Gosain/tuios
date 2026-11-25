package main

import (
	"regexp"
	"strings"
)

// failure is one failure a test runner printed in a failed step.
type failure struct {
	line        int // Where the failure is named.
	first, last int // The lines of its block.
	title       string
	location    string // file:line, when the log says it.
	detail      string
}

var (
	numberedRE     = regexp.MustCompile(`^\s{1,6}(\d+)\) (.+)$`)
	rspecCommandRE = regexp.MustCompile(`^rspec (?:\./)?(\S+?:\d+)(?:\s+#\s*(.*))?$`)
	cucumberRE     = regexp.MustCompile(`^cucumber (\S+?:\d+)(?:\s+#\s*(.*))?$`)
	minitestNameRE = regexp.MustCompile(`^(\S+#\S+) \[([^\]]+:\d+)\]:?$`)
	pytestRE       = regexp.MustCompile(`^FAILED (\S+)(?: - (.*))?$`)
	jestRE         = regexp.MustCompile(`^\s*● (.+ › .+)$`)
	goTestRE       = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	goWhereRE      = regexp.MustCompile(`^\s+(\S+\.go:\d+): (.+)$`)
	annotationRE   = regexp.MustCompile(`^(\S+?)#L(\d+)(?:C\d+)?: (.+)$`)
	exitLinePrefix = "Process completed with exit code"
)

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// findFailures reads lines from to to (inclusive, -1 for the end) for what
// test runners print: RSpec's Failures: section and its rspec lines,
// Cucumber's Failing Scenarios:, Minitest's Failure: blocks, pytest's
// FAILED, Jest's bullet, Go's --- FAIL: and error annotations with a file.
// The runner's own "Process completed with exit code" line is the last
// resort: every failed step ends in it, whatever went wrong.
func findFailures(lines []LogLine, from, to int) []failure {
	if to < 0 || to >= len(lines) {
		to = len(lines) - 1
	}
	from = max(0, from)
	var found, lastResort []failure
	rspecSection := -1
	var rspecOpen []int // Failures of the section that wait for a location.
	cucumberSection := -1
	current := -1 // The RSpec failure whose reason is being read.
	closeCurrent := func(before int) {
		if current >= 0 && found[current].last < 0 {
			found[current].last = before
		}
		current = -1
	}
	for i := from; i <= to; i++ {
		text := lines[i].Text
		trimmed := strings.TrimSpace(text)

		if lines[i].Kind == errorLine {
			closeCurrent(i - 1)
			f := failure{line: i, first: i, last: i, title: text}
			if m := annotationRE.FindStringSubmatch(text); m != nil {
				f.location, f.title = m[1]+":"+m[2], m[3]
			}
			if strings.HasPrefix(text, exitLinePrefix) {
				lastResort = append(lastResort, f)
			} else {
				found = append(found, f)
			}
			continue
		}

		if trimmed == "Failures:" {
			closeCurrent(i - 1)
			rspecSection, rspecOpen, cucumberSection = i, nil, -1
			continue
		}
		if rspecSection >= 0 {
			if m := numberedRE.FindStringSubmatch(text); m != nil {
				closeCurrent(i - 1)
				first := i
				if len(rspecOpen) == 0 {
					first = rspecSection
				}
				found = append(found, failure{line: i, first: first, last: -1, title: strings.TrimSpace(m[2])})
				current = len(found) - 1
				rspecOpen = append(rspecOpen, current)
				continue
			}
			if current >= 0 && found[current].detail == "" && strings.HasPrefix(trimmed, "Failure/Error:") {
				found[current].detail = strings.TrimSpace(strings.TrimPrefix(trimmed, "Failure/Error:"))
				continue
			}
			if strings.HasPrefix(trimmed, "Finished in ") || trimmed == "Failed examples:" {
				// Leave out the blank lines between the reason and the summary.
				end := i - 1
				for end > rspecSection && blank(lines[end].Text) {
					end--
				}
				closeCurrent(end)
				continue
			}
		}
		if m := rspecCommandRE.FindStringSubmatch(trimmed); m != nil {
			if len(rspecOpen) > 0 {
				f := &found[rspecOpen[0]]
				rspecOpen = rspecOpen[1:]
				f.location = m[1]
				// The last failure of a section takes the summary and the
				// rerun commands along, so a copy of it is whole.
				if len(rspecOpen) == 0 || f.last < 0 {
					f.last = i
				}
			} else {
				// The list without the section: a log cut off above it.
				title := m[2]
				if title == "" {
					title = m[1]
				}
				found = append(found, failure{line: i, first: i, last: i, location: m[1], title: title})
			}
			continue
		}

		if trimmed == "Failing Scenarios:" {
			closeCurrent(i - 1)
			cucumberSection, rspecSection = i, -1
			continue
		}
		if cucumberSection >= 0 {
			if m := cucumberRE.FindStringSubmatch(trimmed); m != nil {
				title := m[2]
				if title == "" {
					title = m[1]
				}
				for k := range found {
					if found[k].first == cucumberSection {
						found[k].last = i
					}
				}
				found = append(found, failure{line: i, first: cucumberSection, last: i, location: m[1], title: title})
				continue
			}
			if !blank(text) {
				cucumberSection = -1
			}
		}

		// Minitest: "Failure:" or "Error:" on a line of its own, the test
		// and its file on the next.
		if (trimmed == "Failure:" || trimmed == "Error:") && i < to {
			if m := minitestNameRE.FindStringSubmatch(strings.TrimSpace(lines[i+1].Text)); m != nil {
				end := i + 1
				for end < to && !blank(lines[end+1].Text) {
					end++
				}
				f := failure{line: i, first: i, last: end, title: m[1], location: m[2]}
				if i+2 <= end {
					f.detail = strings.TrimSpace(lines[i+2].Text)
				}
				found = append(found, f)
				i = end
				continue
			}
		}

		if m := pytestRE.FindStringSubmatch(text); m != nil {
			loc, _, _ := strings.Cut(m[1], "::")
			found = append(found, failure{line: i, first: i, last: i, title: strings.TrimSpace(m[1]), location: loc, detail: m[2]})
			continue
		}
		if m := goTestRE.FindStringSubmatch(text); m != nil {
			// go test names the file and the line on the next line, with
			// what it expected.
			f := failure{line: i, first: i, last: i, title: strings.TrimSpace(m[1])}
			if i < to {
				if w := goWhereRE.FindStringSubmatch(lines[i+1].Text); w != nil {
					f.location, f.detail, f.last = w[1], w[2], i+1
				}
			}
			found = append(found, f)
			continue
		}
		if m := jestRE.FindStringSubmatch(text); m != nil {
			found = append(found, failure{line: i, first: i, last: i, title: strings.TrimSpace(m[1])})
		}
	}
	closeCurrent(to)
	for k := range found {
		if found[k].last < found[k].line {
			found[k].last = found[k].line
		}
	}
	if len(found) == 0 {
		return lastResort
	}
	return found
}
