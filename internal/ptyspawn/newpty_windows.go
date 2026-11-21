//go:build windows

package ptyspawn

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/windows"
)

const (
	conptyDLLName   = "conpty.dll"
	openConsoleName = "OpenConsole.exe"
)

func hostPty(width, height int, logf func(string, ...any)) (xpty.Pty, error) {
	dll, err := sideBySideConPTY()
	if err == nil && dll != nil {
		var pty xpty.Pty
		if pty, err = dll.open(width, height); err == nil {
			if logf != nil {
				logf("[PTY] pane on the pseudo console of %s", dll.path)
			}
			return pty, nil
		}
	}
	if err != nil && logf != nil {
		logf("[PTY] %v; pane on the inbox ConPTY", err)
	}
	return xpty.NewPty(width, height)
}

// HostIsConPTY reports whether locally spawned panes run on a Windows ConPTY.
// Both the inbox ConPTY and conpty.dll are one, so their panes keep the kitty
// keyboard protocol off (see vt.Emulator.DisableKittyKeyboardProtocol).
func HostIsConPTY() bool { return true }

var sideBySideConPTY = sync.OnceValues(exeConPTYDLL)

func exeConPTYDLL() (*conptyDLL, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return loadConPTYDLL(filepath.Dir(exe))
}

type conptyDLL struct {
	path                  string
	create, resize, close *windows.Proc
}

func loadConPTYDLL(dir string) (*conptyDLL, error) {
	path := filepath.Join(dir, conptyDLLName)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, nil
	}
	if info, err := os.Stat(filepath.Join(dir, openConsoleName)); err != nil || !info.Mode().IsRegular() {
		return nil, errfmt.Errorf("%s has no %s beside it", path, openConsoleName)
	}
	mod, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, errfmt.Errorf("failed to load %s: %w", path, err)
	}
	lib := &windows.DLL{Name: path, Handle: mod}
	find := func(name string) (*windows.Proc, error) {
		proc, err := lib.FindProc(name)
		if err != nil {
			return nil, errfmt.Errorf("failed to find %s in %s: %w", name, path, err)
		}
		return proc, nil
	}
	dll := &conptyDLL{path: path}
	if dll.create, err = find("CreatePseudoConsole"); err == nil {
		if dll.resize, err = find("ResizePseudoConsole"); err == nil {
			dll.close, err = find("ClosePseudoConsole")
		}
	}
	if err != nil {
		_ = lib.Release()
		return nil, err
	}
	return dll, nil
}

func coord(width, height int) uintptr {
	c := windows.Coord{X: int16(width), Y: int16(height)}
	return uintptr(*(*uint32)(unsafe.Pointer(&c)))
}

func hresult(r uintptr) error {
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

type dllPty struct {
	dll           *conptyDLL
	hpc           windows.Handle
	input, output windows.Handle
	attrList      *windows.ProcThreadAttributeListContainer
	width, height int
	closeOnce     sync.Once
}

var _ xpty.Pty = (*dllPty)(nil)

func (d *conptyDLL) open(width, height int) (_ *dllPty, err error) {
	if width <= 0 || height <= 0 {
		width, height = 80, 25
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, errfmt.Errorf("failed to create input pipe for %s: %w", d.path, err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, errfmt.Errorf("failed to create output pipe for %s: %w", d.path, err)
	}
	p := &dllPty{dll: d, input: inWrite, output: outRead, width: width, height: height}
	r, _, _ := d.create.Call(coord(width, height), uintptr(inRead), uintptr(outWrite), 0, uintptr(unsafe.Pointer(&p.hpc)))
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)
	if err := hresult(r); err != nil {
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, errfmt.Errorf("failed to create a pseudo console on %s: %w", d.path, err)
	}
	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()
	if p.attrList, err = windows.NewProcThreadAttributeList(1); err != nil {
		return nil, errfmt.Errorf("failed to allocate the process attributes: %w", err)
	}
	hpc := *(*unsafe.Pointer)(unsafe.Pointer(&p.hpc))
	if err := p.attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpc, unsafe.Sizeof(p.hpc)); err != nil {
		return nil, errfmt.Errorf("failed to attach the pseudo console to the process attributes: %w", err)
	}
	return p, nil
}

func (p *dllPty) Name() string { return "windows-pty" }

func (p *dllPty) Fd() uintptr { return uintptr(p.hpc) }

func (p *dllPty) Read(b []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(p.output, b, &n, nil)
	return int(n), err
}

func (p *dllPty) Write(b []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(p.input, b, &n, nil)
	return int(n), err
}

func (p *dllPty) Resize(width, height int) error {
	r, _, _ := p.dll.resize.Call(uintptr(p.hpc), coord(width, height))
	if err := hresult(r); err != nil {
		return errfmt.Errorf("failed to resize pseudo console: %w", err)
	}
	p.width, p.height = width, height
	return nil
}

func (p *dllPty) Size() (int, int, error) { return p.width, p.height, nil }

func (p *dllPty) Close() error {
	var err error
	p.closeOnce.Do(func() {
		if p.attrList != nil {
			p.attrList.Delete()
		}
		_, _, _ = p.dll.close.Call(uintptr(p.hpc))
		err = errors.Join(windows.CloseHandle(p.input), windows.CloseHandle(p.output))
	})
	return err
}

func (p *dllPty) Start(cmd *exec.Cmd) error {
	argv0, err := lookExtensions(cmd.Path, cmd.Dir)
	if err != nil {
		return err
	}
	if cmd.Dir != "" {
		if argv0, err = joinExeDirAndFName(cmd.Dir, argv0); err != nil {
			return err
		}
	}
	cmdline := windows.ComposeCommandLine(cmd.Args)
	var flags uint32
	if sys := cmd.SysProcAttr; sys != nil {
		if sys.CmdLine != "" {
			cmdline = sys.CmdLine
		}
		flags = sys.CreationFlags
	}
	argv0p, err := windows.UTF16PtrFromString(argv0)
	if err != nil {
		return err
	}
	cmdlinep, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		return err
	}
	var dirp *uint16
	if cmd.Dir != "" {
		if dirp, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return err
		}
	}
	var env []uint16
	for _, kv := range cmd.Environ() {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			return err
		}
		env = append(env, u...)
	}
	env = append(env, 0)

	si := new(windows.StartupInfoEx)
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = p.attrList.List()
	flags |= windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(argv0p, cmdlinep, nil, nil, false, flags, &env[0], dirp, &si.StartupInfo, &pi); err != nil {
		return errfmt.Errorf("failed to create process: %w", err)
	}
	defer func() {
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
	}()

	if cmd.Process, err = os.FindProcess(int(pi.ProcessId)); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return errfmt.Errorf("failed to find process after starting: %w", err)
	}
	return nil
}

func lookExtensions(path, dir string) (string, error) {
	if filepath.Base(path) == path {
		path = filepath.Join(".", path)
	}
	if dir == "" || filepath.VolumeName(path) != "" || (len(path) > 1 && os.IsPathSeparator(path[0])) {
		return exec.LookPath(path)
	}
	dirandpath := filepath.Join(dir, path)
	lp, err := exec.LookPath(dirandpath)
	if err != nil {
		return "", err
	}
	return path + strings.TrimPrefix(lp, dirandpath), nil
}

func isSlash(c uint8) bool { return c == '\\' || c == '/' }

func normalizeDir(dir string) (string, error) {
	ndir, err := syscall.FullPath(dir)
	if err != nil {
		return "", err
	}
	if len(ndir) > 2 && isSlash(ndir[0]) && isSlash(ndir[1]) {
		return "", syscall.EINVAL
	}
	return ndir, nil
}

func volToUpper(ch int) int {
	if 'a' <= ch && ch <= 'z' {
		ch += 'A' - 'a'
	}
	return ch
}

func joinExeDirAndFName(dir, p string) (string, error) {
	if len(p) == 0 {
		return "", syscall.EINVAL
	}
	if len(p) > 2 && isSlash(p[0]) && isSlash(p[1]) {
		return p, nil
	}
	if len(p) > 1 && p[1] == ':' {
		if len(p) == 2 {
			return "", syscall.EINVAL
		}
		if isSlash(p[2]) {
			return p, nil
		}
		d, err := normalizeDir(dir)
		if err != nil {
			return "", err
		}
		if volToUpper(int(p[0])) == volToUpper(int(d[0])) {
			return syscall.FullPath(d + "\\" + p[2:])
		}
		return syscall.FullPath(p)
	}
	d, err := normalizeDir(dir)
	if err != nil {
		return "", err
	}
	if isSlash(p[0]) {
		return syscall.FullPath(d[:2] + p)
	}
	return syscall.FullPath(d + "\\" + p)
}
