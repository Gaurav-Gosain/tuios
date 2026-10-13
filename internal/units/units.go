// Package units writes sizes and durations the same way wherever tuios shows
// them.
package units

import (
	"fmt"
	"strconv"
	"time"
)

// Bytes writes a size in the binary units: 512 B, 1.50 KiB, 12.3 MiB,
// 2.00 GiB.
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
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f %siB", v, units)
	case v >= 10:
		return fmt.Sprintf("%.1f %siB", v, units)
	}
	return fmt.Sprintf("%.2f %siB", v, units)
}

// ShortBytes writes a size with no space and one decimal: 512B, 1.5KB, 3.2MB.
func ShortBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
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
