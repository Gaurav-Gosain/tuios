package herdrplugin

// The reaper: a small tuios process that leads each plugin command's
// process session.
//
// The daemon knows a plugin's process by its kernel session id, which is the
// pid of the session's leader (Runner.Owner). That answer stays true only
// while the pid cannot name another session. The kernel does not hand out a
// pid while a live process has it, so the runner starts each command under
// the reaper, which leads the session and lives until nothing the command
// started is left. Its exit is the moment the session is empty, and the
// runner drops the owner entry then. No timer runs in the daemon for it.
//
// On Linux the reaper is a child subreaper (PR_SET_CHILD_SUBREAPER): every
// process the command leaves behind is reparented to it, so it waits for
// them all and exits when it has no child left. macOS has no subreaper. There
// the reaper looks at its process group once a second, and only after the
// command itself has ended while something it started still runs.
//
// The reaper tells the runner when the command itself exits, on fd 3, so a
// job's log, exit code and slot do not wait for what the command left
// behind.

// ReaperArg is the first argument that makes tuios run as a reaper:
// tuios ReaperArg PROGRAM ARGS...
const ReaperArg = "__plugin-reaper"
