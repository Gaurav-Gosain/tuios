package sound

// candidates is the macOS probe order. afplay ships with the system and plays a
// WAV with no arguments, so the list is one entry long and the fallbacks are
// there only for a machine where someone removed it.
func candidates() []candidate {
	return []candidate{
		{name: "afplay"},
		{name: "ffplay", flags: []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
		{name: "mpv", flags: []string{"--no-video", "--really-quiet"}},
	}
}
