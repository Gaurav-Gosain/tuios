//go:build !darwin && !windows

package sound

// candidates is the Linux and BSD probe order.
//
// The audio-server clients come first because they mix: a cue played through
// PipeWire or PulseAudio lands alongside whatever else is making noise, and
// respects the volume the user set for notifications. aplay talks to ALSA
// directly, which is the right answer on a box with no server and the wrong one
// on a box that has one, so it sits below them and above the media players.
//
// The media players are last because they are the heaviest thing on the list
// and the least likely to be installed for this purpose. ffplay and mpv both
// need telling not to open a window and not to print a status line.
//
// Every cue is a WAV, which is the reason this list can be short and can
// include aplay at all. A compressed asset would need decoding, and the ALSA
// and PipeWire tools do not decode: aplay handed an MP3 plays the file's bytes
// as raw samples, at full volume, for as long as the file is.
func candidates() []candidate {
	return []candidate{
		{name: "paplay"},
		{name: "pw-play"},
		{name: "aplay", flags: []string{"-q"}},
		{name: "ffplay", flags: []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
		{name: "mpv", flags: []string{"--no-video", "--really-quiet"}},
	}
}
