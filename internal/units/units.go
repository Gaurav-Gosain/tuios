// Package units writes sizes and durations the same way wherever tuios shows
// them.
package units

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Bytes writes a size in the binary units, to three significant digits at
// most, with no trailing zeros: 512 B, 1.5 KiB, 12.3 MiB, 2 GiB.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	units := "KMGTP"[exp : exp+1]
	prec := 2
	switch {
	case v >= 100:
		prec = 0
	case v >= 10:
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if prec > 0 {
		s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	}
	return s + " " + units + "iB"
}

// Duration writes a time span to the second: 17s, 3m 05s, 1h 02m.
func Duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds()+0.5)) + "s"
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// Waited writes how long something has waited, short enough for a column:
// 40s, 12m, 3h, 2d. since is Unix nanoseconds; a zero or negative one is
// "?", and a time in the future counts as no wait.
func Waited(since int64, now time.Time) string {
	if since <= 0 {
		return "?"
	}
	d := max(now.Sub(time.Unix(0, since)), 0)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}
