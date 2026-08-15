// Package sound plays the two short cues an agent alert can make audible.
//
// It shells out to an audio player the system already has rather than linking a
// decoder and a device backend into tuios. A multiplexer that fails to start
// because a machine has no sound card would be a poor trade for a 300 ms chime,
// and every platform tuios runs on ships something that plays a WAV: paplay or
// pw-play under PipeWire and PulseAudio, aplay under bare ALSA, afplay on
// macOS, and the Media Foundation player Windows carries.
//
// Three properties matter more than the sound itself.
//
// Nothing here blocks its caller. Play is a bounds check, an atomic compare and
// a non-blocking channel send; every process spawn happens on one background
// goroutine. It is called from the bubbletea Update goroutine, which owns the
// frame budget, and a caller that has to wait for an audio device is a caller
// that has already lost.
//
// A machine with no audio goes quiet permanently rather than repeatedly. The
// player list is resolved once, and a run of plays that all fail switches the
// subsystem off for the life of the process, so an SSH session or a container
// pays for one failed probe rather than one per alert. Nothing is printed: a
// user who cannot hear the cue has not asked to be told about it in the middle
// of their terminal.
//
// One cue plays at a time and no faster than the cooldown. A single worker and
// a one-deep queue mean a workspace where six agents finish together makes one
// sound, not six overlapping ones.
package sound

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Cue names a sound. There are two on purpose: the pair has to be told apart by
// ear in under half a second, and a third would only be guessed at.
type Cue int

const (
	// CueDone is the agent having stopped. It is information, so it is quiet.
	CueDone Cue = iota
	// CueAttention is the agent waiting on a human, or having failed. It is a
	// request, so it is higher, longer and louder.
	CueAttention
)

// DisableEnv silences the package however it is configured. It exists for the
// cases where config is the wrong lever: a test suite, a CI job, a recording.
const DisableEnv = "TUIOS_NO_SOUND"

// playTimeout bounds one player invocation. A player that hangs on a wedged
// device must not pin the worker, and no cue is longer than half a second, so
// anything near this is already a failure.
const playTimeout = 15 * time.Second

// failuresBeforeGivingUp is how many plays may fail every player in the list
// before the subsystem switches itself off. More than one because an audio
// server restarting is a transient every desktop sees, and few because a box
// with no device will never succeed and should stop spawning processes.
const failuresBeforeGivingUp = 3

// Request is one cue to make audible.
type Request struct {
	// Cue selects the sound.
	Cue Cue
	// File replaces the embedded asset with a file of the user's own. An empty
	// or unreadable path falls back to the embedded cue, so a typo in config
	// costs the custom sound rather than all sound.
	File string
	// Cooldown is the shortest gap between two audible cues. Zero plays every
	// request the queue accepts.
	Cooldown time.Duration
}

var (
	queue  = make(chan Request, 1)
	launch sync.Once
	// lastAt is the unix-nano time of the last accepted request, read and
	// written by Play alone.
	lastAt atomic.Int64
	// off is set once the subsystem has decided it cannot make a sound on this
	// machine. It is never cleared: the reason is structural, not momentary.
	off atomic.Bool
)

// Play makes a cue audible, or does nothing.
//
// It never blocks and never reports failure. A request arriving while another
// is still playing is dropped rather than queued, because a cue that plays after
// the state it announced has already changed is worse than no cue at all.
func Play(r Request) {
	if off.Load() || os.Getenv(DisableEnv) != "" {
		return
	}
	if !accept(time.Now().UnixNano(), r.Cooldown) {
		return
	}
	launch.Do(func() { go worker() })
	select {
	case queue <- r:
	default:
	}
}

// accept claims the cooldown slot for now and reports whether this request is
// the one that gets to make a sound. The compare-and-swap is what makes two
// panes finishing in the same instant produce one cue rather than two.
func accept(now int64, cooldown time.Duration) bool {
	if cooldown <= 0 {
		lastAt.Store(now)
		return true
	}
	last := lastAt.Load()
	if now-last < int64(cooldown) {
		return false
	}
	return lastAt.CompareAndSwap(last, now)
}

// worker owns every process spawn and every temp file. Being the only consumer
// is what lets the caches below need no lock.
func worker() {
	players := resolvePlayers()
	if len(players) == 0 {
		off.Store(true)
		return
	}
	spilled := map[Cue]string{}
	best := 0
	fails := 0

	for r := range queue {
		file := r.File
		if file == "" || !readable(file) {
			path, err := spill(spilled, r.Cue)
			if err != nil {
				continue
			}
			file = path
		}
		played, next := run(players, best, file)
		if played {
			best, fails = next, 0
			continue
		}
		fails++
		if fails >= failuresBeforeGivingUp {
			off.Store(true)
			return
		}
	}
}

// run tries the players in order, starting with the one that worked last, and
// reports whether any of them played the file and which one did.
func run(players []player, start int, file string) (bool, int) {
	for i := range players {
		idx := (start + i) % len(players)
		if playWith(players[idx], file) {
			return true, idx
		}
	}
	return false, start
}

// playWith runs one player to completion under the timeout. Its streams are left
// nil so the child gets /dev/null: a player that writes to the terminal would
// paint over a TUI in raw mode, and a player whose pipe fills would deadlock.
func playWith(p player, file string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), playTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.path, p.argv(file)...) //nolint:gosec // path came from LookPath over a fixed list
	if p.env != nil {
		cmd.Env = append(os.Environ(), p.env(file)...)
	}
	return cmd.Run() == nil
}

// spill writes an embedded cue to a file a player can open, once per cue per
// process. Players take a path, not a stream, and a fresh temp file per alert
// would be a write and an unlink for every sound.
func spill(cache map[Cue]string, c Cue) (string, error) {
	if path, ok := cache[c]; ok && readable(path) {
		return path, nil
	}
	dir, err := os.MkdirTemp("", "tuios-sound-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, cueFiles[c])
	if err := os.WriteFile(path, cueAssets[c], 0o600); err != nil {
		return "", err
	}
	cache[c] = path
	return path, nil
}

func readable(path string) bool {
	f, err := os.Open(path) //nolint:gosec // a path the user put in their own config
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// player is a resolved audio program and the arguments that make it play one
// file quietly and exit.
type player struct {
	path string
	argv func(file string) []string
	// env, when set, adds variables to the child's environment. Windows uses it
	// to hand the path to a PowerShell script without going through argv, where
	// a quote in a filename would be a command.
	env func(file string) []string
}

// candidate is one program to look for, before it is known to exist. flags are
// placed before the file path unless argv replaces the whole list.
type candidate struct {
	name  string
	flags []string
	argv  func(file string) []string
	env   func(file string) []string
}

// resolvePlayers walks the platform's candidate list once and keeps the ones
// that exist. It is called once per process: a binary that is not on PATH at
// startup will not appear later, and re-walking the list on every alert is the
// cost a machine with no audio would pay forever.
func resolvePlayers() []player {
	var found []player
	for _, c := range candidates() {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		argv := c.argv
		if argv == nil {
			flags := c.flags
			argv = func(file string) []string { return append(append([]string{}, flags...), file) }
		}
		found = append(found, player{path: path, argv: argv, env: c.env})
	}
	return found
}
