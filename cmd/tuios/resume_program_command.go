package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// newResumeProgramCommand is the command a restore types into a restored
// shell to bring back a program that reported how to resume it (OSC 7577).
// The daemon writes the program's argv and folder to a one-use file and
// types only this binary's path, the command and the file's path, so no
// shell ever parses what the program reported. See
// internal/session/program_resume_restore.go.
func newResumeProgramCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "resume-program FILE",
		Short:  "Start the program a restore resumes, from its one-use file",
		Hidden: true,
		// The shell's environment is the program's: nothing here may read
		// a config or reach the daemon. The daemon names FILE by an absolute
		// path, so it never reads as a flag.
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runResumeProgram(args[0])
		},
	}
}

// resumeProgramFile is the file the daemon writes. See
// session.programResumeFile.
type resumeProgramFile struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd,omitempty"`
	Dir  string   `json:"dir,omitempty"`
}

// runResumeProgram reads and removes the file, then becomes the program: on
// the PATH of this process, which is the shell's, in the folder the program
// named.
func runResumeProgram(path string) error {
	spec, err := readResumeProgramFile(path)
	if err != nil {
		return err
	}
	if spec.Cwd != "" {
		if err := os.Chdir(spec.Cwd); err != nil {
			return errfmt.Errorf("tuios cannot resume %s: the folder %s is gone", spec.Argv[0], spec.Cwd)
		}
	} else if spec.Dir != "" {
		// A folder the owner was in, read by the daemon. It is a hint: a
		// folder that is gone leaves the shell's.
		_ = os.Chdir(spec.Dir)
	}
	bin := spec.Argv[0]
	if !strings.HasPrefix(bin, "/") {
		bin, err = exec.LookPath(spec.Argv[0])
		if err != nil {
			return errfmt.Errorf("tuios cannot resume %s: it is not on PATH", spec.Argv[0])
		}
	}
	return execProgram(bin, spec.Argv)
}

// readResumeProgramFile reads the one-use file and removes it. A file that
// another user owns, or that others may read, is refused.
func readResumeProgramFile(path string) (resumeProgramFile, error) {
	var spec resumeProgramFile
	info, err := os.Lstat(path)
	if err != nil {
		return spec, errfmt.Errorf("tuios cannot resume the program: %w", err)
	}
	// Removed before it is read, so a failure below still uses it once.
	defer func() { _ = os.Remove(path) }()
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return spec, errors.New("tuios cannot resume the program: the resume file is not private")
	}
	if uid, ok := procinfo.FileOwner(info); ok && uid != os.Getuid() {
		return spec, errors.New("tuios cannot resume the program: another user owns the resume file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spec, errfmt.Errorf("tuios cannot resume the program: %w", err)
	}
	if err := jsonx.Unmarshal(data, &spec); err != nil || len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return spec, errors.New("tuios cannot resume the program: the resume file is not valid")
	}
	return spec, nil
}
