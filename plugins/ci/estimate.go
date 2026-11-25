package main

import (
	"math"
	"slices"
)

// How long a build will still take, from how long the earlier builds took.
// Nothing here knows about GitHub, processes or the clock.

func quantile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return -1
	}
	v := slices.Clone(values)
	slices.Sort(v)
	at := q * float64(len(v)-1)
	below := int(math.Floor(at))
	above := min(below+1, len(v)-1)
	return v[below] + (v[above]-v[below])*(at-float64(below))
}

// likeBuilds are the earlier builds this one is compared with, newest
// first: finished, older and like it. On the same branch when at least
// three of those passed, else started by the same kind of event, so a pull
// request is compared with other pull requests, else all of them.
func likeBuilds(all []Build, b Build, most int) []Build {
	var sameBranch, sameEvent, every []Build
	passedBranch, passedEvent := 0, 0
	for _, o := range all {
		if o.Key == b.Key || !o.IsBuild || active(o.State) || o.State == "skipped" || o.Started.After(b.Started) {
			continue
		}
		passed := o.State == "success"
		every = append(every, o)
		if o.Event == b.Event {
			sameEvent = append(sameEvent, o)
			if passed {
				passedEvent++
			}
		}
		if o.Branch == b.Branch {
			sameBranch = append(sameBranch, o)
			if passed {
				passedBranch++
			}
		}
	}
	chosen := every
	switch {
	case passedBranch >= 3:
		chosen = sameBranch
	case passedEvent >= 3:
		chosen = sameEvent
	}
	if len(chosen) > most {
		chosen = chosen[:most]
	}
	return chosen
}

// spread is how long the builds that passed took: the lower quartile, the
// median and the upper quartile.
type spread struct {
	samples           int
	low, typical, top float64
}

func (s spread) known() bool { return s.samples >= 3 }

func spreadOf(like []Build) spread {
	var secs []float64
	for _, b := range like {
		if b.State == "success" {
			secs = append(secs, b.Seconds)
		}
	}
	s := spread{samples: len(secs)}
	if len(secs) > 0 {
		s.low, s.typical, s.top = quantile(secs, .25), quantile(secs, .5), quantile(secs, .75)
	}
	return s
}

// jobTime is one job, with times in seconds since its build started. -1 is
// unknown.
type jobTime struct {
	key        string
	state      string // running, queued, success, failure, cancelled, skipped or pending.
	start, end float64
}

// earlier is one earlier build: the jobs that ran in it.
type earlier map[string]jobTime

type jobForecast struct {
	key       string
	expected  bool // GitHub has not made the job yet. Earlier builds say it comes.
	start     float64
	end       float64
	low, high float64 // The end with every job quick, and with every job slow.
	typical   float64 // How long the job usually takes.
	late      bool    // It runs for longer than it usually does.
}

type forecast struct {
	known     bool
	basis     string // "jobs" when played forward job by job, "builds" from whole builds.
	samples   int
	elapsed   float64
	total     float64
	low, high float64
	late      bool    // Past the slow end of what the earlier builds did.
	over      float64 // How far past the usual end.
	jobs      []jobForecast
}

func ran(j jobTime) bool { return j.start >= 0 && j.end >= j.start && j.state != "skipped" }

type habit struct {
	lengths, offsets []float64
	after            map[string]bool // Jobs that had always ended before this one started.
	seen             int
}

func done(s string) bool { return !active(s) && s != "pending" }

// forecastOf plays the build forward from the jobs as they are now. A
// running job ends at its start plus its usual length. A job that has not
// started begins when the jobs it waits for end. The build ends when the
// last job does. It does that three times, with every job quick, usual and
// slow.
func forecastOf(jobs []jobTime, elapsed float64, builds spread, past []earlier) forecast {
	out := forecast{elapsed: elapsed}

	// Only earlier builds that did what this one does say anything about
	// it: once the tests run, a build that skipped them is another build.
	var alike []earlier
	for _, e := range past {
		fits := true
		for _, j := range jobs {
			if j.state == "running" || j.state == "success" || j.state == "failure" {
				if o, ok := e[j.key]; !ok || !ran(o) {
					fits = false
				}
			}
		}
		if fits {
			alike = append(alike, e)
		}
	}

	habits := map[string]*habit{}
	for _, e := range alike {
		for key, j := range e {
			if !ran(j) {
				continue
			}
			h := habits[key]
			if h == nil {
				h = &habit{}
				habits[key] = h
			}
			h.lengths = append(h.lengths, j.end-j.start)
			h.offsets = append(h.offsets, j.start)
			before := map[string]bool{}
			for other, o := range e {
				if other != key && ran(o) && o.end <= j.start+2 {
					before[other] = true
				}
			}
			if h.seen == 0 {
				h.after = before
			} else {
				for k := range h.after {
					if !before[k] {
						delete(h.after, k)
					}
				}
			}
			h.seen++
		}
	}

	// The jobs GitHub lists, and the ones it makes only once what they wait
	// for is done.
	all := slices.Clone(jobs)
	states := map[string]string{}
	for _, j := range jobs {
		states[j.key] = j.state
	}
	var expected []string
	for key, h := range habits {
		if _, ok := states[key]; !ok && h.seen*2 > len(alike) {
			expected = append(expected, key)
		}
	}
	slices.SortFunc(expected, func(a, b string) int {
		x, y := quantile(habits[a].offsets, .5), quantile(habits[b].offsets, .5)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	})
	dropped := map[string]bool{}
	for _, key := range expected {
		// A job that waits for one that failed, was cancelled or was
		// skipped does not run.
		blocked := false
		for need := range habits[key].after {
			if s := states[need]; s == "failure" || s == "cancelled" || s == "skipped" || dropped[need] {
				blocked = true
			}
		}
		if blocked {
			dropped[key] = true
			continue
		}
		all = append(all, jobTime{key: key, state: "pending", start: -1, end: -1})
	}

	qs := [3]float64{.25, .5, .75}
	var ends [3]map[string]float64
	for i := range ends {
		ends[i] = map[string]float64{}
	}
	// The same ends without holding a running job to "not before now". That
	// tells a build that is busy from one that is overdue.
	var unheld [3]float64
	complete := len(all) > 0
	order := slices.Clone(all)
	rank := func(j jobTime) float64 {
		if j.start >= 0 && j.state != "queued" && j.state != "pending" {
			return j.start
		}
		if h, ok := habits[j.key]; ok {
			return quantile(h.offsets, .5)
		}
		return 1e12
	}
	slices.SortStableFunc(order, func(a, b jobTime) int {
		x, y := rank(a), rank(b)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})

	forecasts := map[string]jobForecast{}
	for _, j := range order {
		f := jobForecast{key: j.key, expected: j.state == "pending", start: -1, typical: -1}
		h, has := habits[j.key]
		if has {
			f.typical = quantile(h.lengths, .5)
		}
		var res [3]float64
		for q := range 3 {
			switch {
			case done(j.state):
				f.start = j.start
				res[q] = j.end
				if j.state == "skipped" {
					res[q] = max(0, j.start)
				}
			case j.state == "running" && j.start >= 0:
				f.start = j.start
				if !has {
					complete = false
					res[q] = elapsed
				} else {
					usual := j.start + quantile(h.lengths, qs[q])
					unheld[q] = max(unheld[q], usual)
					res[q] = max(elapsed, usual)
				}
			default:
				if !has {
					complete = false
					res[q] = elapsed
					continue
				}
				start, waits := elapsed, false
				for need := range h.after {
					if e, ok := ends[q][need]; ok {
						start, waits = max(start, e), true
					}
				}
				if !waits {
					start = max(start, quantile(h.offsets, .5))
				}
				if q == 1 {
					f.start = start
				}
				res[q] = start + quantile(h.lengths, qs[q])
			}
			ends[q][j.key] = res[q]
			if j.state != "running" {
				unheld[q] = max(unheld[q], res[q])
			}
		}
		f.low, f.end, f.high = res[0], res[1], res[2]
		f.late = j.state == "running" && has && f.typical >= 0 && elapsed-j.start > f.typical+1
		forecasts[j.key] = f
	}
	for _, j := range all {
		out.jobs = append(out.jobs, forecasts[j.key])
	}
	latest := func(q int) float64 {
		end := elapsed
		for _, v := range ends[q] {
			end = max(end, v)
		}
		return end
	}
	switch {
	case complete && len(alike) >= 2:
		out.known, out.basis, out.samples = true, "jobs", len(alike)
		out.total = latest(1)
		out.low = min(latest(0), out.total)
		out.high = max(latest(2), out.total)
		out.late = elapsed > unheld[2]+1
		out.over = max(0, elapsed-unheld[1])
	case builds.known():
		out.known, out.basis, out.samples = true, "builds", builds.samples
		out.total = max(elapsed, builds.typical)
		out.low = min(builds.low, out.total)
		out.high = max(builds.top, out.total)
		out.late = elapsed > builds.top+1
		out.over = max(0, elapsed-builds.typical)
	}
	return out
}

// rounded is a time rounded to what a forecast can be trusted to: five
// seconds under a minute, ten under five minutes, half a minute under a
// quarter of an hour, else a minute.
func rounded(seconds float64) float64 {
	step := 60.0
	switch {
	case seconds < 60:
		step = 5
	case seconds < 300:
		step = 10
	case seconds < 900:
		step = 30
	}
	return math.Round(seconds/step) * step
}
