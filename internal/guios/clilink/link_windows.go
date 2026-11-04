package clilink

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// On Windows the command is a copy of the app's executable that runs in
// the console: the app's runs without one (the GUI subsystem), so shells
// would neither wait for it nor show what it prints. kero.target names the
// executable it is a copy of, as it was then.

func target(bin string) string {
	b, err := os.ReadFile(filepath.Join(bin, "kero.target"))
	if err != nil {
		return ""
	}
	exe, _, _ := strings.Cut(string(b), "\n")
	return exe
}

// stamp tells an executable's version: its path, size and time.
func stamp(exe string) (string, error) {
	st, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n%d\n%d\n", exe, st.Size(), st.ModTime().UnixNano()), nil
}

func link(exe, bin string) error {
	if strings.EqualFold(filepath.Dir(exe), filepath.Clean(bin)) {
		return nil // the command itself, which started kerod
	}
	want, err := stamp(exe)
	if err != nil {
		return err
	}
	cmd := filepath.Join(bin, "kero.exe")
	if have, err := os.ReadFile(filepath.Join(bin, "kero.target")); err == nil && string(have) == want {
		if _, err := os.Stat(cmd); err == nil {
			return nil
		}
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	if b, err = consoleExecutable(b); err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	tmp := cmd + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	// A running executable can't be replaced, but it can be moved: the
	// last one goes aside, to be removed once nothing runs it.
	olds, _ := filepath.Glob(filepath.Join(bin, "kero.exe.old*"))
	for _, o := range olds {
		os.Remove(o)
	}
	if _, err := os.Stat(cmd); err == nil {
		if err := os.Rename(cmd, fmt.Sprintf("%s.old%d", cmd, os.Getpid())); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, cmd); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(bin, "kero.target"), []byte(want), 0o644)
}

// consoleExecutable turns a GUI executable into a console one: its
// subsystem, and without its signature, which the change breaks.
func consoleExecutable(b []byte) ([]byte, error) {
	bad := errors.New("clilink: not a Windows executable")
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return nil, bad
	}
	pe := int(binary.LittleEndian.Uint32(b[0x3c:]))
	opt := pe + 24 // the optional header, after the signature and file header
	if pe < 0 || opt+128 > len(b) || !bytes.Equal(b[pe:pe+4], []byte("PE\x00\x00")) {
		return nil, bad
	}
	var dirs int // the data directories
	switch binary.LittleEndian.Uint16(b[opt:]) {
	case 0x10b: // PE32
		dirs = opt + 96
	case 0x20b: // PE32+
		dirs = opt + 112
	default:
		return nil, bad
	}
	const guiSubsystem, consoleSubsystem = 2, 3
	if binary.LittleEndian.Uint16(b[opt+68:]) == guiSubsystem {
		binary.LittleEndian.PutUint16(b[opt+68:], consoleSubsystem)
	}
	// The certificate table, the fifth directory: a file offset, at the
	// end of the file.
	sec := dirs + 4*8
	if sec+8 <= len(b) {
		off := int(binary.LittleEndian.Uint32(b[sec:]))
		size := int(binary.LittleEndian.Uint32(b[sec+4:]))
		if off > 0 && size > 0 && off+size == len(b) {
			b = b[:off]
			binary.LittleEndian.PutUint64(b[sec:], 0)
		}
	}
	return b, nil
}
