package main

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Gaurav-Gosain/tuios/internal/guios/clilink"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// cliBin is the directory of the kero command, which kerod keeps and
// sessions have on their PATH.
func cliBin() string { return filepath.Join(proto.Dir(), "bin") }

// installCLI puts the kero command on the user's PATH, for terminals
// started from now on.
func (a *App) installCLI() {
	exe, err := os.Executable()
	if err != nil {
		a.toast(err.Error())
		return
	}
	go func() {
		err := clilink.Link(exe, cliBin())
		if err == nil {
			err = addToUserPath(cliBin())
		}
		a.update(func() {
			if err != nil {
				a.toast(locales.CliInstallFailed())
				return
			}
			a.toast(locales.CliAddedToPath())
		})
	}()
}

// cliInstalled returns where the kero command is installed, and what it
// runs, "" when the user's PATH lacks it.
func cliInstalled() string {
	if !onUserPath(cliBin()) {
		return ""
	}
	cmd := filepath.Join(cliBin(), "kero.exe")
	if t := clilink.Target(cliBin()); t != "" {
		return proto.ShortDir(cmd) + " → " + proto.ShortDir(t)
	}
	return proto.ShortDir(cmd)
}

func userPath() (registry.Key, string, uint32, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return 0, "", 0, err
	}
	v, typ, err := k.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return k, "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		k.Close()
		return 0, "", 0, err
	}
	return k, v, typ, nil
}

func onUserPath(dir string) bool {
	k, path, _, err := userPath()
	if err != nil {
		return false
	}
	k.Close()
	for _, p := range filepath.SplitList(path) {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}

// addToUserPath appends dir to the user's PATH, and tells the shell, so
// that what Explorer starts next has it.
func addToUserPath(dir string) error {
	if onUserPath(dir) {
		return nil
	}
	k, path, typ, err := userPath()
	if err != nil {
		return err
	}
	defer k.Close()
	if path != "" && !strings.HasSuffix(path, ";") {
		path += ";"
	}
	path += dir
	if typ == registry.SZ {
		err = k.SetStringValue("Path", path)
	} else {
		err = k.SetExpandStringValue("Path", path)
	}
	if err != nil {
		return err
	}
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xFFFF, 0x001A, 0x0002
	user32 := windows.NewLazySystemDLL("user32.dll")
	var result uintptr
	user32.NewProc("SendMessageTimeoutW").Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&result)))
	return nil
}
