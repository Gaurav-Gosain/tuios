package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"tuios.dev/vt"
)

// The preflight: before anything is paused, the new binary is run once, on
// the very file the exec will use, and asked which manifests it reads. A
// binary that cannot run, or reads another version, is refused with nothing
// changed. The run also pays the cost of a first exec of a new file (0.3 to
// 0.5 s on macOS) outside the pause, and proves that the new binary starts at
// all: package init and flag parsing are the code that runs first after the
// exec. See docs/design/live-handoff.md, section 2.1.

// HandoffAccepts is what a tuios binary says about the handoffs it can take,
// as `tuios daemon handoff-accepts` prints it.
type HandoffAccepts struct {
	// Handoff is the manifest version range it reads, oldest first.
	Handoff [2]int `json:"handoff"`
	// Protocol and Verb are the binary and JSON protocol ranges its daemon
	// serves, which say whether a client can reattach.
	Protocol [2]int `json:"protocol"`
	Verb     [2]int `json:"verb"`
	Backend  string `json:"backend"`
	Version  string `json:"version"`
	OS       string `json:"os"`
}

// LocalHandoffAccepts is what this binary takes.
func LocalHandoffAccepts(version string) HandoffAccepts {
	return HandoffAccepts{
		Handoff:  [2]int{MinHandoffVersion, HandoffVersion},
		Protocol: [2]int{MinProtocolVersion, ProtocolVersion},
		Verb:     [2]int{MinVerbProtocolVersion, VerbProtocolVersion},
		Backend:  vt.Backend,
		Version:  version,
		OS:       runtime.GOOS,
	}
}

// handoffPreflightTimeout bounds the preflight run. A first exec of a new
// file is slow on macOS, and a machine under load slower still.
const handoffPreflightTimeout = 30 * time.Second

// handoffBinary is a binary a preflight ran: its path and the file it was
// when it ran, which the exec must still find there.
type handoffBinary struct {
	path     string
	file     fileID
	accepts  HandoffAccepts
	duration time.Duration
}

// fileID names a file by device and inode.
type fileID struct{ dev, ino uint64 }

// preflightBinary runs path's handoff-accepts and checks it reads the
// manifest this build writes.
func preflightBinary(path string) (handoffBinary, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return handoffBinary{}, err
	}
	before, err := statFileID(abs)
	if err != nil {
		return handoffBinary{}, fmt.Errorf("%s: %w", abs, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), handoffPreflightTimeout)
	defer cancel()
	var out, errOut bytes.Buffer
	//tuios:allow-exec the preflight runs the new binary once, before anything is paused
	cmd := exec.CommandContext(ctx, abs, "daemon", "handoff-accepts")
	cmd.Stdout, cmd.Stderr = &out, &errOut
	started := time.Now()
	if err := cmd.Run(); err != nil {
		detail := bytes.TrimSpace(errOut.Bytes())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return handoffBinary{}, fmt.Errorf("%s does not run as a tuios that can take this daemon's state: %v %s", abs, err, detail)
	}
	took := time.Since(started)
	var acc HandoffAccepts
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &acc); err != nil {
		return handoffBinary{}, fmt.Errorf("%s answered handoff-accepts with something that is not its answer: %q", abs, bytes.TrimSpace(out.Bytes()))
	}
	if acc.OS != runtime.GOOS {
		return handoffBinary{}, fmt.Errorf("%s is a tuios for %s", abs, acc.OS)
	}
	if HandoffVersion < acc.Handoff[0] || HandoffVersion > acc.Handoff[1] {
		return handoffBinary{}, fmt.Errorf("%s (%s) cannot read this daemon's state (handoff v%d, it reads v%d to v%d)",
			abs, acc.Version, HandoffVersion, acc.Handoff[0], acc.Handoff[1])
	}
	after, err := statFileID(abs)
	if err != nil || after != before {
		return handoffBinary{}, fmt.Errorf("%s was replaced while it was being checked", abs)
	}
	return handoffBinary{path: abs, file: after, accepts: acc, duration: took}, nil
}

// stillThere reports whether the binary's path still names the file the
// preflight ran.
func (b handoffBinary) stillThere() bool {
	id, err := statFileID(b.path)
	return err == nil && id == b.file
}

var errNotAnExecutable = errors.New("not a regular executable file")

// statFileID is the device and inode of the executable at path.
func statFileID(path string) (fileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileID{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fileID{}, errNotAnExecutable
	}
	return fileIDOf(info), nil
}
