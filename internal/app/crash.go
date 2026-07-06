package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/adrg/xdg"
)

// CrashLogDir returns the directory for crash logs.
func CrashLogDir() string {
	return filepath.Join(xdg.StateHome, "tuios")
}

// WriteCrashLog writes a crash report to a timestamped file in the crash log directory.
// Returns the path to the written file, or empty string on failure.
func WriteCrashLog(panicValue any, stack []byte) string {
	dir := CrashLogDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return ""
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("crash-%s.log", timestamp)
	path := filepath.Join(dir, filename)

	var content string
	content += "tuios crash report\n"
	content += "==================\n\n"
	content += fmt.Sprintf("Time:    %s\n", time.Now().Format(time.RFC3339))
	content += fmt.Sprintf("Go:      %s\n", runtime.Version())
	content += fmt.Sprintf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if info, ok := debug.ReadBuildInfo(); ok {
		content += fmt.Sprintf("Module:  %s\n", info.Main.Path)
		content += fmt.Sprintf("Version: %s\n", info.Main.Version)
	}
	content += fmt.Sprintf("\nPanic:   %v\n\n", panicValue)
	content += fmt.Sprintf("Stack trace:\n%s\n", stack)
	content += "\n---\nPlease report this issue at:\n"
	content += fmt.Sprintf("https://github.com/Gaurav-Gosain/tuios/issues/new?title=Crash%%3A+%v\n", panicValue)

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return ""
	}
	return path
}
