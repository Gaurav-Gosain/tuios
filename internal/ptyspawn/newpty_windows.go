//go:build windows

package ptyspawn

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/windows"
)

// conptyDLLName is the side-by-side ConPTY from the
// Microsoft.Windows.Console.ConPTY package. It starts the openConsoleName in
// its own folder, and the inbox conhost when there is none.
const (
	conptyDLLName   = "conpty.dll"
	openConsoleName = "OpenConsole.exe"
)

// hostPty opens a pane on the conpty.dll beside tuios.exe when there is one,
// and on the ConPTY built into Windows otherwise.
//
// The inbox ConPTY runs conhost, which parses the guest's output and draws it
// again. It drops sixel images, and it answers DA1 itself, without sixel and
// ahead of the terminal, so a program that ends its probe of the terminal
// with DA1, as yazi does, never reads tuios's cell size (CSI 16t). The
// OpenConsole that conpty.dll starts passes the images and the queries
// through.
//
// A DLL that is there but fails to load or to make a pseudo console costs the
// pane its images, not its shell: the pane falls back to the inbox ConPTY.
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

// sideBySideConPTY is the conpty.dll beside tuios.exe, loaded on first use and
// kept for the life of the process. Tests point it at a folder of their own.
var sideBySideConPTY = sync.OnceValues(exeConPTYDLL)

// exeConPTYDLL loads conpty.dll from the folder of the running executable,
// after symlinks, which is where a package puts it beside tuios.exe. The
// working directory and PATH are never searched: a pane can be started from
// any folder, and a DLL found there would run in the daemon.
func exeConPTYDLL() (*conptyDLL, error) {
	exe, err := os.Executable()
	if err != nil {
		// With no folder to look in there is no DLL to use.
		return nil, nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return loadConPTYDLL(filepath.Dir(exe))
}

// conptyDLL is a loaded conpty.dll. It exports the pseudo console functions
// under the same names and signatures as kernel32.
type conptyDLL struct {
	path                  string
	create, resize, close *windows.Proc
}

// loadConPTYDLL loads dir's conpty.dll. It returns nil and no error when
// there is none. The DLL's own imports resolve from its folder and System32
// only, so nothing on PATH or in the working directory is pulled in with it.
//
// A conpty.dll without its OpenConsole.exe is refused: it would start the
// inbox conhost, which gains the pane nothing, while the log named the DLL.
func loadConPTYDLL(dir string) (*conptyDLL, error) {
	path := filepath.Join(dir, conptyDLLName)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, nil
	}
	if info, err := os.Stat(filepath.Join(dir, openConsoleName)); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s has no %s beside it", path, openConsoleName)
	}
	mod, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", path, err)
	}
	lib := &windows.DLL{Name: path, Handle: mod}
	find := func(name string) (*windows.Proc, error) {
		proc, err := lib.FindProc(name)
		if err != nil {
			return nil, fmt.Errorf("failed to find %s in %s: %w", name, path, err)
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

// coord packs a COORD into the one register the pseudo console functions take
// it in by value.
func coord(width, height int) uintptr {
	c := windows.Coord{X: int16(width), Y: int16(height)}
	return uintptr(*(*uint32)(unsafe.Pointer(&c)))
}

// hresult turns a returned HRESULT into an error, as x/sys/windows does for
// kernel32's CreatePseudoConsole.
func hresult(r uintptr) error {
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// dllPty is xpty.ConPty with the pseudo console calls going to the DLL.
// xpty.ConPty has no seam for the swap, so its pipe setup and Start are
// adapted from github.com/charmbracelet/x/conpty v0.2.0 (conpty_windows.go),
// MIT licensed; the licence is in LICENSE.conpty.
type dllPty struct {
	dll           *conptyDLL
	hpc           windows.Handle
	input, output windows.Handle // the guest's input, written here, and its output, read here
	attrList      *windows.ProcThreadAttributeListContainer
	width, height int
	closeOnce     sync.Once
}

var _ xpty.Pty = (*dllPty)(nil)

// open makes a width x height pseudo console on the DLL and the attribute
// list Start hands to CreateProcess. On failure nothing is left open.
func (d *conptyDLL) open(width, height int) (_ *dllPty, err error) {
	if width <= 0 || height <= 0 {
		width, height = 80, 25
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to create input pipe for %s: %w", d.path, err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("failed to create output pipe for %s: %w", d.path, err)
	}
	p := &dllPty{dll: d, input: inWrite, output: outRead, width: width, height: height}
	r, _, _ := d.create.Call(coord(width, height), uintptr(inRead), uintptr(outWrite), 0, uintptr(unsafe.Pointer(&p.hpc)))
	// The pseudo console holds its own copies of its ends of the pipes.
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)
	if err := hresult(r); err != nil {
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("failed to create a pseudo console on %s: %w", d.path, err)
	}
	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()
	if p.attrList, err = windows.NewProcThreadAttributeList(1); err != nil {
		return nil, fmt.Errorf("failed to allocate the process attributes: %w", err)
	}
	// The attribute's value is the HPCON itself, a pointer the DLL allocated,
	// so its bits are passed as one.
	hpc := *(*unsafe.Pointer)(unsafe.Pointer(&p.hpc))
	if err := p.attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpc, unsafe.Sizeof(p.hpc)); err != nil {
		return nil, fmt.Errorf("failed to attach the pseudo console to the process attributes: %w", err)
	}
	return p, nil
}

// Name matches xpty.ConPty's.
func (p *dllPty) Name() string { return "windows-pty" }

// Fd returns the pseudo console handle, as xpty.ConPty does.
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
		return fmt.Errorf("failed to resize pseudo console: %w", err)
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

// Start starts cmd on the pseudo console. Like xpty.ConPty it ignores
// cmd.Stdin, Stdout and Stderr: the child's standard handles are the console.
func (p *dllPty) Start(cmd *exec.Cmd) error {
	// CreateProcess resolves a relative program against the daemon's current
	// directory, not cmd.Dir, and adds no extension, so the path is made
	// absolute here the way conpty's Spawn does for the inbox pane.
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
	// Environ dedups the variables and adds SYSTEMROOT, as exec.Cmd.Start does.
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
	// Empty standard handles keep the child off the daemon's own, so it talks
	// to the pseudo console.
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = p.attrList.List()
	flags |= windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(argv0p, cmdlinep, nil, nil, false, flags, &env[0], dirp, &si.StartupInfo, &pi); err != nil {
		return fmt.Errorf("failed to create process: %w", err)
	}
	defer func() {
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
	}()

	// FindProcess opens a handle of its own, so the process stays known by
	// it after ours are closed.
	if cmd.Process, err = os.FindProcess(int(pi.ProcessId)); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return fmt.Errorf("failed to find process after starting: %w", err)
	}
	return nil
}

// The helpers below are adapted from conpty v0.2.0 (exec_windows.go), which
// copies them from the Go standard library's os/exec and syscall, BSD
// licensed; the licence is in LICENSE.golang. They keep a pane's program
// resolved the same way on both ConPTYs, whether it is relative to cmd.Dir,
// rooted on its drive (\Windows\...), on another drive or a UNC path, with
// the extension LookPath adds.

func lookExtensions(path, dir string) (string, error) {
	if filepath.Base(path) == path {
		path = filepath.Join(".", path)
	}
	if dir == "" || filepath.VolumeName(path) != "" || (len(path) > 1 && os.IsPathSeparator(path[0])) {
		return exec.LookPath(path)
	}
	dirandpath := filepath.Join(dir, path)
	// LookPath only adds the extension.
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
		// dir cannot have \\server\share\path form
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
		// \\server\share\path form
		return p, nil
	}
	if len(p) > 1 && p[1] == ':' {
		// has drive letter
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
	// no drive letter
	d, err := normalizeDir(dir)
	if err != nil {
		return "", err
	}
	if isSlash(p[0]) {
		return syscall.FullPath(d[:2] + p)
	}
	return syscall.FullPath(d + "\\" + p)
}
