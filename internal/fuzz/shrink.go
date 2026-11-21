package fuzz

import "tuios.dev/vt/vtgen"

func shrink(as []Action, still func([]Action) bool, obs Observer, minW, minH int) []Action {
	return vtgen.Reduce(as, still, func(a Action) []Action { return simpler(a, minW, minH) }, obs.Shrink)
}

func simpler(a Action, minW, minH int) []Action {
	var out []Action
	if a.Kind != Tick {
		out = append(out, Action{Kind: Tick})
	}
	if a.S != "" {
		switch a.Kind {
		case Rename, Text:
			out = append(out, Action{Kind: a.Kind, A: a.A, B: a.B, C: a.C, S: "a"})
		case Guest:
			out = append(out, Action{Kind: a.Kind, A: a.A, B: a.B, C: a.C, S: "x"})
		}
	}
	for _, f := range []func(Action) Action{
		func(x Action) Action { x.A, x.B = 0, 0; return x },
		func(x Action) Action { x.A /= 2; return x },
		func(x Action) Action { x.B /= 2; return x },
		func(x Action) Action { x.A, x.B = x.A/2, x.B/2; return x },
	} {
		c := f(a)
		if c.Kind == Resize {
			c.A, c.B = max(c.A, minW), max(c.B, minH)
		}
		if c != a {
			out = append(out, c)
		}
	}
	return out
}
