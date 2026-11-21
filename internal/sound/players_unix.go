//go:build !darwin && !windows

package sound

func candidates() []candidate {
	return []candidate{
		{name: "paplay"},
		{name: "pw-play"},
		{name: "aplay", flags: []string{"-q"}},
		{name: "ffplay", flags: []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
		{name: "mpv", flags: []string{"--no-video", "--really-quiet"}},
	}
}
