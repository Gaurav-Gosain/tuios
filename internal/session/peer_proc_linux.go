//go:build linux

package session

import (
	"strconv"
	"strings"
)

// parseStatField extracts one numeric field, numbered as proc(5) numbers them,
// from the contents of a /proc/<pid>/stat line. The comm field (2) is wrapped
// in parentheses and may itself contain spaces or parentheses, so the numeric
// fields are parsed from after the final ')'.
func parseStatField(s string, field int) (int, bool) {
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 >= len(s) || field < 3 {
		return 0, false
	}
	// Fields after "(comm) ": state(3) ppid(4) pgrp(5) session(6) tty_nr(7)
	// tpgid(8). Splitting the remainder puts field n at index n-3.
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < field-2 {
		return 0, false
	}
	v, err := strconv.Atoi(fields[field-3])
	if err != nil {
		return 0, false
	}
	return v, true
}
