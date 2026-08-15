package sound

// winScript plays the file named by the environment variable below and waits
// for it to finish. SoundPlayer handles WAV specifically and synchronously,
// which is all the cues need, and it is present on every supported Windows
// without a media stack being installed.
//
// The path arrives in the environment rather than in the script text because a
// filename is user data and PowerShell reads its arguments as code. A cue file
// named with a backtick or a quote would otherwise be a command.
const winScript = `$p=[Environment]::GetEnvironmentVariable('TUIOS_SOUND_PATH');` +
	`$s=New-Object System.Media.SoundPlayer $p;$s.PlaySync()`

// candidates is the Windows probe order. PowerShell first because it is
// guaranteed; pwsh for a machine that has only the cross-platform build.
func candidates() []candidate {
	argv := func(string) []string {
		return []string{"-NoProfile", "-NonInteractive", "-Command", winScript}
	}
	env := func(file string) []string { return []string{"TUIOS_SOUND_PATH=" + file} }
	return []candidate{
		{name: "powershell", argv: argv, env: env},
		{name: "pwsh", argv: argv, env: env},
		{name: "ffplay", flags: []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
		{name: "mpv", flags: []string{"--no-video", "--really-quiet"}},
	}
}
