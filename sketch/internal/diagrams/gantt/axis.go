// Ported from pele src/diagrams/gantt/axis.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// pele's Calendar is an object of closures, and `every` wraps one in another. The port
// keeps a Calendar as a struct: the unit and the step say which floor and offset apply.
// pele's `range` is tickRange, and its `pad` is padFill, as dates.go has a pad of its own.

package gantt

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

// Time axis support, following d3-time and d3-time-format (English locale, local time), which Mermaid uses:
// the `axisFormat` specifiers, tick intervals with d3's `every` rule, and its choice of automatic ticks.

const (
	SECOND = 1000
	MINUTE = 60 * SECOND
	HOUR   = 60 * MINUTE
	DAY    = 24 * HOUR
	WEEK   = 7 * DAY
	MONTH  = 30 * DAY
	YEAR   = 365 * DAY
)

type TickUnit int

const (
	millisecond TickUnit = iota
	second
	minute
	hour
	day
	week
	month
	year
)

var tickUnitNames = [...]string{"millisecond", "second", "minute", "hour", "day", "week", "month", "year"}

type Calendar struct {
	unit TickUnit
	// The step `every` was given; 1 for the unit's own calendar.
	step      float64
	weekStart float64
	// For weeks taken several at a time: the first week start at or before the epoch.
	epoch js.Date
	// How many values `field` cycles through, which bounds how far apart filtered ticks can be.
	cycle float64
	ms    float64
}

func startOfDay(d *js.Date) {
	d.SetHours(0, 0, 0, 0)
}

func weekCalendar(weekStart float64) Calendar {
	return Calendar{unit: week, step: 1, weekStart: weekStart, cycle: math.Inf(1), ms: WEEK}
}

func baseCalendar(unit TickUnit) Calendar {
	c := Calendar{unit: unit, step: 1, cycle: math.Inf(1)}
	switch unit {
	case millisecond:
		c.ms = 1
	case second:
		c.cycle, c.ms = 60, SECOND
	case minute:
		c.cycle, c.ms = 60, MINUTE
	case hour:
		c.cycle, c.ms = 24, HOUR
	case day:
		c.cycle, c.ms = 31, DAY
	case month:
		c.cycle, c.ms = 12, MONTH
	case year:
		c.ms = YEAR
	}
	return c
}

// CALENDARS is CALENDARS[unit].
func CALENDARS(unit TickUnit) Calendar { return baseCalendar(unit) }

func (c *Calendar) baseFloor(d *js.Date) {
	switch c.unit {
	case second:
		d.SetTime(d.T - d.Milliseconds())
	case minute:
		d.SetTime(d.T - d.Milliseconds() - d.Seconds()*SECOND)
	case hour:
		d.SetTime(d.T - d.Milliseconds() - d.Seconds()*SECOND - d.Minutes()*MINUTE)
	case day:
		startOfDay(d)
	case week:
		d.SetDate(d.Date() - math.Mod(d.Day()+7-c.weekStart, 7))
		startOfDay(d)
	case month:
		d.SetDate(1)
		startOfDay(d)
	case year:
		d.SetMonth(0, 1)
		startOfDay(d)
	}
}

func (c *Calendar) baseOffset(d *js.Date, step float64) {
	switch c.unit {
	case millisecond:
		d.SetTime(d.T + step)
	case second:
		d.SetTime(d.T + step*SECOND)
	case minute:
		d.SetTime(d.T + step*MINUTE)
	case hour:
		d.SetTime(d.T + step*HOUR)
	case day:
		d.SetDate(d.Date() + step)
	case week:
		d.SetDate(d.Date() + step*7)
	case month:
		d.SetMonth(d.Month()+step, math.NaN())
	case year:
		d.SetFullYear(d.FullYear() + step)
	}
}

// The value d3 tests against a step: a tick falls where it is a multiple of the step.
func (c *Calendar) field(d js.Date) float64 {
	switch c.unit {
	case second:
		return d.UTCSeconds()
	case minute:
		return d.Minutes()
	case hour:
		return d.Hours()
	case day:
		return d.Date() - 1
	case month:
		return d.Month()
	}
	return math.NaN()
}

func (c *Calendar) test(d js.Date) bool { return math.Mod(c.field(d), c.step) == 0 }

func finite(d js.Date) bool { return !math.IsNaN(d.T) && !math.IsInf(d.T, 0) }

func (c *Calendar) floor(d *js.Date) {
	step := c.step
	if step <= 1 {
		c.baseFloor(d)
		return
	}
	switch c.unit {
	case millisecond:
		d.SetTime(math.Floor(d.T/step) * step)
	case year:
		d.SetFullYear(math.Floor(d.FullYear()/step) * step)
		c.baseFloor(d)
	case week:
		c.baseFloor(d)
		c.baseOffset(d, -mod(span(c.epoch, *d, WEEK), step))
	default:
		// Date overflow makes field(d) NaN, which can never match the interval.
		for c.baseFloor(d); finite(*d) && !c.test(*d); c.baseFloor(d) {
			d.SetTime(d.T - 1)
		}
	}
}

func (c *Calendar) offset(d *js.Date, n float64) {
	step := c.step
	if step <= 1 {
		c.baseOffset(d, n)
		return
	}
	switch c.unit {
	case millisecond:
		d.SetTime(d.T + n*step)
	case year:
		d.SetFullYear(d.FullYear() + n*step)
	case week:
		c.baseOffset(d, n*step)
	default:
		for i := 0.0; i < n && finite(*d); i++ {
			c.baseOffset(d, 1)
			for finite(*d) && !c.test(*d) {
				c.baseOffset(d, 1)
			}
		}
	}
}

// Whole units between two floored dates, discounting a change of UTC offset between them.
func span(start, end js.Date, unit float64) float64 {
	return math.Floor((end.T - start.T - (end.TimezoneOffset()-start.TimezoneOffset())*MINUTE) / unit)
}

func mod(a, b float64) float64 {
	return math.Mod(math.Mod(a, b)+b, b)
}

// The interval d3's `unit.every(step)` gives.
func every(unit TickUnit, step float64, weekStart float64, loc *time.Location) Calendar {
	var base Calendar
	if unit == week {
		base = weekCalendar(weekStart)
	} else {
		base = CALENDARS(unit)
	}
	if step <= 1 {
		return base
	}
	base.step = step
	if unit == week {
		epoch := js.NewDate(0, loc)
		base.baseFloor(&epoch)
		base.epoch = epoch
	}
	return base
}

// Ticks from start to stop inclusive, or nil and false when there would be more than `limit`.
func tickRange(calendar *Calendar, start, stop float64, limit int, loc *time.Location) ([]float64, bool) {
	d := js.NewDate(start-1, loc)
	calendar.floor(&d)
	calendar.offset(&d, 1)
	calendar.floor(&d)
	out := []float64{}
	previous := math.Inf(-1)
	for d.T <= stop && d.T > previous {
		if len(out) >= limit {
			return nil, false
		}
		previous = d.T
		out = append(out, previous)
		calendar.offset(&d, 1)
		calendar.floor(&d)
	}
	return out, true
}

func tickStep(start, stop, count float64) float64 {
	step := (stop - start) / math.Max(0, count)
	power := math.Floor(math.Log10(step))
	err := step / math.Pow(10, power)
	factor := 1.0
	if err >= math.Sqrt(50) {
		factor = 10
	} else if err >= math.Sqrt(10) {
		factor = 5
	} else if err >= math.Sqrt2 {
		factor = 2
	}
	return math.Pow(10, power) * factor
}

type autoTick struct {
	unit TickUnit
	step float64
	ms   float64
}

var AUTO = [...]autoTick{
	{second, 1, SECOND},
	{second, 5, 5 * SECOND},
	{second, 15, 15 * SECOND},
	{second, 30, 30 * SECOND},
	{minute, 1, MINUTE},
	{minute, 5, 5 * MINUTE},
	{minute, 15, 15 * MINUTE},
	{minute, 30, 30 * MINUTE},
	{hour, 1, HOUR},
	{hour, 3, 3 * HOUR},
	{hour, 6, 6 * HOUR},
	{hour, 12, 12 * HOUR},
	{day, 1, DAY},
	{day, 2, 2 * DAY},
	{week, 1, WEEK},
	{month, 1, MONTH},
	{month, 3, 3 * MONTH},
	{year, 1, YEAR},
}

// About `count` ticks at round times, chosen the way d3's time scale chooses them.
func autoTicks(start, stop float64, count int, loc *time.Location) []float64 {
	target := (stop - start) / float64(count)
	i := 0
	for i < len(AUTO) && AUTO[i].ms <= target {
		i++
	}
	var calendar Calendar
	if i == len(AUTO) {
		calendar = every(year, math.Max(1, tickStep(start/YEAR, stop/YEAR, float64(count))), 0, loc)
	} else if i == 0 {
		calendar = every(millisecond, math.Max(tickStep(start, stop, float64(count)), 1), 0, loc)
	} else {
		pick := AUTO[i]
		if target/AUTO[i-1].ms < AUTO[i].ms/target {
			pick = AUTO[i-1]
		}
		calendar = every(pick.unit, pick.step, 0, loc)
	}
	ticks, ok := tickRange(&calendar, start, stop, 4*count+8, loc)
	if !ok {
		return []float64{}
	}
	return ticks
}

var WEEK_STARTS = [7]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// RE_INTERVAL: /^([1-9]\d*)(millisecond|second|minute|hour|day|week|month)$/. Returns the step and
// the unit, or false.
func matchInterval(interval string) (float64, TickUnit, bool) {
	q := 0
	for q < len(interval) && interval[q] >= '0' && interval[q] <= '9' {
		q++
	}
	if q == 0 || interval[0] == '0' {
		return 0, 0, false
	}
	for unit := millisecond; unit <= month; unit++ {
		if interval[q:] == tickUnitNames[unit] {
			return js.ParseIntPrefix(interval[:q], 10), unit, true
		}
	}
	return 0, 0, false
}

// Ticks for a `tickInterval` such as `1week`, or false when it is not one or asks for more than `limit`.
func intervalTicks(interval, weekday string, start, stop float64, limit int, loc *time.Location) ([]float64, bool) {
	step, unit, ok := matchInterval(interval)
	if !ok {
		return nil, false
	}
	weekStart := -1
	for i, name := range WEEK_STARTS {
		if name == weekday {
			weekStart = i
			break
		}
	}
	calendar := every(unit, step, float64(max(0, weekStart)), loc)
	if (stop-start)/(math.Min(step, calendar.cycle)*calendar.ms) > float64(limit) {
		return nil, false
	}
	return tickRange(&calendar, start, stop, limit, loc)
}

func padFill(value float64, fill string, width int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := js.Num(value)
	if len(digits) < width {
		return sign + strings.Repeat(fill, width-len(digits)) + digits
	}
	return sign + digits
}

func floored(date js.Date, calendar Calendar) js.Date {
	d := date
	calendar.floor(&d)
	return d
}

// Weeks since the last `weekStart` day before the year began, which is d3's %U and %W.
func weekOfYear(date js.Date, weekStart float64) float64 {
	wk := weekCalendar(weekStart)
	yearStart := floored(date, CALENDARS(year))
	return span(floored(js.NewDate(yearStart.T-1, date.Loc), wk), floored(date, wk), WEEK)
}

// The Thursday that decides which ISO week and ISO year a date belongs to.
func isoThursday(date js.Date) js.Date {
	thursday := weekCalendar(4)
	dd := date.Day()
	from := date
	if !(dd >= 4 || dd == 0) {
		from = js.NewDate(date.T-1, date.Loc)
	}
	d := floored(from, thursday)
	if !(dd >= 4 || dd == 0) {
		thursday.offset(&d, 1)
		thursday.floor(&d)
	}
	return d
}

// PADS is PADS.get(c).
func PADS(c string) (string, bool) {
	switch c {
	case "-":
		return "", true
	case "_":
		return " ", true
	case "0":
		return "0", true
	}
	return "", false
}

// charAt is specifier.charAt(i) for a byte offset: the character there, or "" past the end.
func charAt(s string, i int) string {
	if i >= len(s) {
		return ""
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return s[i : i+size]
}

// Formats a date with a d3 time format specifier such as `%Y-%m-%d`.
func timeFormat(specifier string, date js.Date) string {
	var out strings.Builder
	from := 0
	n := len(specifier)
	for i := 0; i < n; i++ {
		if specifier[i] != 37 {
			continue
		}
		out.WriteString(specifier[from:i])
		i++
		c := charAt(specifier, i)
		fill, ok := PADS(c)
		if ok {
			i += len(c)
			c = charAt(specifier, i)
		} else if c == "e" {
			fill = " "
		} else {
			fill = "0"
		}
		out.WriteString(directive(c, date, fill))
		i += max(len(c), 1) - 1
		from = min(i+1, n)
	}
	out.WriteString(specifier[from:])
	return out.String()
}

// nameAt is names[i], cut to n characters when n > 0. An invalid date has no name; pele
// fails there with a TypeError, and the port gives "".
func nameAt(names []string, i float64, n int) string {
	if !(i >= 0 && i < float64(len(names))) {
		return ""
	}
	name := names[int(i)]
	if n > 0 {
		return name[:n]
	}
	return name
}

func directive(c string, d js.Date, p string) string {
	switch c {
	case "a":
		return nameAt(WEEKDAYS[:], d.Day(), 3)
	case "A":
		return nameAt(WEEKDAYS[:], d.Day(), 0)
	case "b":
		return nameAt(MONTHS[:], d.Month(), 3)
	case "B":
		return nameAt(MONTHS[:], d.Month(), 0)
	case "c":
		return timeFormat("%x, %X", d)
	case "d", "e":
		return padFill(d.Date(), p, 2)
	case "f":
		return padFill(d.Milliseconds(), p, 3) + "000"
	case "g":
		return padFill(math.Mod(isoThursday(d).FullYear(), 100), p, 2)
	case "G":
		return padFill(math.Mod(isoThursday(d).FullYear(), 10000), p, 4)
	case "H":
		return padFill(d.Hours(), p, 2)
	case "I":
		return padFill(js.Or(math.Mod(d.Hours(), 12), 12), p, 2)
	case "j":
		return padFill(1+span(floored(d, CALENDARS(year)), floored(d, CALENDARS(day)), DAY), p, 3)
	case "L":
		return padFill(d.Milliseconds(), p, 3)
	case "m":
		return padFill(d.Month()+1, p, 2)
	case "M":
		return padFill(d.Minutes(), p, 2)
	case "p":
		if d.Hours() >= 12 {
			return "PM"
		}
		return "AM"
	case "q":
		return js.Num(1 + math.Floor(d.Month()/3))
	case "Q":
		return js.Num(d.T)
	case "s":
		return js.Num(math.Floor(d.T / 1000))
	case "S":
		return padFill(d.Seconds(), p, 2)
	case "u":
		return js.Num(js.Or(d.Day(), 7))
	case "U":
		return padFill(weekOfYear(d, 0), p, 2)
	case "V":
		thursday := isoThursday(d)
		yearStart := floored(thursday, CALENDARS(year))
		first := floored(yearStart, weekCalendar(4))
		extra := 0.0
		if yearStart.Day() == 4 {
			extra = 1
		}
		return padFill(span(first, thursday, WEEK)+extra, p, 2)
	case "w":
		return js.Num(d.Day())
	case "W":
		return padFill(weekOfYear(d, 1), p, 2)
	case "x":
		return timeFormat("%-m/%-d/%Y", d)
	case "X":
		return timeFormat("%-I:%M:%S %p", d)
	case "y":
		return padFill(math.Mod(d.FullYear(), 100), p, 2)
	case "Y":
		return padFill(math.Mod(d.FullYear(), 10000), p, 4)
	case "Z":
		z := d.TimezoneOffset()
		abs := math.Abs(z)
		sign := "+"
		if z > 0 {
			sign = "-"
		}
		return sign + padFill(math.Floor(abs/60), "0", 2) + padFill(math.Mod(abs, 60), "0", 2)
	case "%":
		return "%"
	default:
		return c
	}
}
