package sound

const winScript = `$p=[Environment]::GetEnvironmentVariable('TUIOS_SOUND_PATH');` +
	`$s=New-Object System.Media.SoundPlayer $p;$s.PlaySync()`

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
