package js

import (
	"math"
	"time"
)

// Date is a JavaScript Date: a time value in milliseconds since the epoch, NaN when
// invalid, read and set in the local time of Loc as JavaScript reads it in the
// process's time zone. The calendar arithmetic follows ECMAScript's abstract
// operations (Day, MakeDay, MakeTime, LocalTime, UTC, TimeClip) and V8's limits, so
// that values out of the usual range behave as they do in V8.
type Date struct {
	T   float64
	Loc *time.Location
}

const (
	msPerSecond = 1000.0
	msPerMinute = 60000.0
	msPerHour   = 3600000.0
	msPerDay    = 86400000.0
	maxTime     = 8.64e15
)

// NewDate is `new Date(t)` for a number: the time value clipped to the valid range.
func NewDate(t float64, loc *time.Location) Date {
	return Date{timeClip(t), loc}
}

// LocalDate is `new Date(y, m, d, h, min, s, ms)`: the fields read in local time, with
// a two-digit year counted from 1900.
func LocalDate(loc *time.Location, y, m, d, h, min, s, ms float64) Date {
	y = fullYear(y)
	return Date{timeClip(utc(makeDate(makeDay(y, m, d), makeTime(h, min, s, ms)), loc)), loc}
}

// DateUTC is Date.UTC(y, m, d, h, min, s, ms).
func DateUTC(y, m, d, h, min, s, ms float64) float64 {
	y = fullYear(y)
	return timeClip(makeDate(makeDay(y, m, d), makeTime(h, min, s, ms)))
}

func fullYear(y float64) float64 {
	if !math.IsNaN(y) {
		yi := toInteger(y)
		if yi >= 0 && yi <= 99 {
			return 1900 + yi
		}
	}
	return y
}

// Valid is !Number.isNaN(date.getTime()).
func (d Date) Valid() bool { return !math.IsNaN(d.T) }

func toInteger(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	if math.IsInf(x, 0) {
		return x
	}
	return math.Trunc(x) + 0
}

func timeClip(t float64) float64 {
	if math.IsNaN(t) || math.IsInf(t, 0) || math.Abs(t) > maxTime {
		return math.NaN()
	}
	return toInteger(t)
}

func day(t float64) float64 { return math.Floor(t / msPerDay) }

func timeWithinDay(t float64) float64 {
	r := math.Mod(t, msPerDay)
	if r < 0 {
		r += msPerDay
	}
	return r
}

func dayFromYear(y float64) float64 {
	return 365*(y-1970) + math.Floor((y-1969)/4) - math.Floor((y-1901)/100) + math.Floor((y-1601)/400)
}

func timeFromYear(y float64) float64 { return msPerDay * dayFromYear(y) }

func yearFromTime(t float64) float64 {
	y := math.Floor(t/(msPerDay*365.2425)) + 1970
	for timeFromYear(y) > t {
		y--
	}
	for timeFromYear(y+1) <= t {
		y++
	}
	return y
}

func inLeapYear(y float64) bool {
	return math.Mod(y, 4) == 0 && (math.Mod(y, 100) != 0 || math.Mod(y, 400) == 0)
}

// The first day of each month in a common year, and one past the last.
var monthStart = [13]float64{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365}

// monthDate gives the month (0 to 11) and the day of the month of a time value.
func monthDate(t float64) (float64, float64) {
	y := yearFromTime(t)
	dayInYear := day(t) - dayFromYear(y)
	leap := 0.0
	if inLeapYear(y) {
		leap = 1
	}
	for m := 0; m < 12; m++ {
		end := monthStart[m+1]
		if m >= 1 {
			end += leap
		}
		if dayInYear < end {
			start := monthStart[m]
			if m >= 2 {
				start += leap
			}
			return float64(m), dayInYear - start + 1
		}
	}
	return 11, dayInYear - monthStart[11] - leap + 1
}

func makeTime(h, m, s, ms float64) float64 {
	for _, v := range [4]float64{h, m, s, ms} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return math.NaN()
		}
	}
	return toInteger(h)*msPerHour + toInteger(m)*msPerMinute + toInteger(s)*msPerSecond + toInteger(ms)
}

// makeDay is MakeDay with V8's bounds on the year and the month.
func makeDay(year, month, date float64) float64 {
	if math.IsNaN(year) || math.IsInf(year, 0) || math.IsNaN(month) || math.IsInf(month, 0) || math.IsNaN(date) || math.IsInf(date, 0) {
		return math.NaN()
	}
	y := toInteger(year)
	m := toInteger(month)
	dt := toInteger(date)
	if y < -1000000 || y > 1000000 || m < -10000000 || m > 10000000 {
		return math.NaN()
	}
	ym := y + math.Floor(m/12)
	mn := math.Mod(m, 12)
	if mn < 0 {
		mn += 12
	}
	first := dayFromYear(ym) + monthStart[int(mn)]
	if mn >= 2 && inLeapYear(ym) {
		first++
	}
	return first + dt - 1
}

func makeDate(d, t float64) float64 {
	if math.IsNaN(d) || math.IsInf(d, 0) || math.IsNaN(t) || math.IsInf(t, 0) {
		return math.NaN()
	}
	return d*msPerDay + t
}

// offsetAt is the zone's offset from UTC in milliseconds at a time value.
func offsetAt(t float64, loc *time.Location) float64 {
	if math.IsNaN(t) || loc == nil {
		return 0
	}
	_, off := time.UnixMilli(int64(t)).In(loc).Zone()
	return float64(off) * msPerSecond
}

func localTime(t float64, loc *time.Location) float64 {
	return t + offsetAt(t, loc)
}

// utc is UTC(t) for a local time value. A local time that a change of offset skips or
// repeats takes the offset in force before the change, as V8 does.
func utc(t float64, loc *time.Location) float64 {
	if math.IsNaN(t) || math.IsInf(t, 0) || loc == nil {
		return t
	}
	before := offsetAt(t-msPerDay, loc)
	if offsetAt(t-before, loc) == before {
		return t - before
	}
	after := offsetAt(t+msPerDay, loc)
	if offsetAt(t-after, loc) == after {
		return t - after
	}
	return t - before
}

func (d Date) local() float64 { return localTime(d.T, d.Loc) }

// FullYear is getFullYear.
func (d Date) FullYear() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	return yearFromTime(d.local())
}

// Month is getMonth.
func (d Date) Month() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	m, _ := monthDate(d.local())
	return m
}

// Date is getDate.
func (d Date) Date() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	_, dt := monthDate(d.local())
	return dt
}

// Day is getDay.
func (d Date) Day() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	w := math.Mod(day(d.local())+4, 7)
	if w < 0 {
		w += 7
	}
	return w
}

func (d Date) timePart(unit, cycle float64) float64 {
	if !d.Valid() {
		return math.NaN()
	}
	return math.Mod(math.Floor(timeWithinDay(d.local())/unit), cycle)
}

// Hours is getHours.
func (d Date) Hours() float64 { return d.timePart(msPerHour, 24) }

// Minutes is getMinutes.
func (d Date) Minutes() float64 { return d.timePart(msPerMinute, 60) }

// Seconds is getSeconds.
func (d Date) Seconds() float64 { return d.timePart(msPerSecond, 60) }

// Milliseconds is getMilliseconds.
func (d Date) Milliseconds() float64 { return d.timePart(1, 1000) }

// UTCSeconds is getUTCSeconds.
func (d Date) UTCSeconds() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	return math.Mod(math.Floor(timeWithinDay(d.T)/msPerSecond), 60)
}

// TimezoneOffset is getTimezoneOffset. V8 divides whole milliseconds by a minute as
// integers, so an offset with seconds in it, such as a zone's local mean time, is cut
// toward zero.
func (d Date) TimezoneOffset() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	return math.Trunc((d.T-d.local())/msPerMinute) + 0
}

// SetTime is setTime.
func (d *Date) SetTime(t float64) { d.T = timeClip(t) }

func (d *Date) setLocal(local float64) { d.T = timeClip(utc(local, d.Loc)) }

// SetDate is setDate.
func (d *Date) SetDate(dt float64) {
	if !d.Valid() {
		return
	}
	t := d.local()
	m, _ := monthDate(t)
	d.setLocal(makeDate(makeDay(yearFromTime(t), m, dt), timeWithinDay(t)))
}

// SetMonth is setMonth(m), or setMonth(m, dt) when dt is not NaN.
func (d *Date) SetMonth(m, dt float64) {
	if !d.Valid() {
		return
	}
	t := d.local()
	if math.IsNaN(dt) {
		_, dt = monthDate(t)
	}
	d.setLocal(makeDate(makeDay(yearFromTime(t), m, dt), timeWithinDay(t)))
}

// SetFullYear is setFullYear(y). An invalid date counts from +0.
func (d *Date) SetFullYear(y float64) {
	t := 0.0
	if d.Valid() {
		t = d.local()
	}
	m, dt := monthDate(t)
	d.setLocal(makeDate(makeDay(y, m, dt), timeWithinDay(t)))
}

// SetHours is setHours(h, m, s, ms).
func (d *Date) SetHours(h, m, s, ms float64) {
	if !d.Valid() {
		return
	}
	t := d.local()
	d.setLocal(makeDate(day(t), makeTime(h, m, s, ms)))
}
