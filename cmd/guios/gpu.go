package main

import (
	"os"
	"runtime"
)

// preferGPU picks the GPU guios draws with on Linux, before GTK loads
// OpenGL. On a machine with NVIDIA's driver and Mesa both installed, EGL
// loads NVIDIA's by default, which costs about 35 MB more of anonymous memory
// than Mesa's for a window this simple (measured 93 MB against 57 MB). A
// terminal needs no discrete GPU, so "integrated" takes Mesa when it is
// there. "auto" leaves EGL to choose: where the displays hang off the NVIDIA
// card, Mesa finds no GPU it can draw on and falls back to llvmpipe, and
// MyGo then draws on the CPU. A vendor the person set in the environment
// always wins.
func preferGPU(choice string) {
	if runtime.GOOS != "linux" || choice != "integrated" {
		return
	}
	if os.Getenv("__EGL_VENDOR_LIBRARY_FILENAMES") != "" || os.Getenv("__EGL_VENDOR_LIBRARY_DIRS") != "" {
		return
	}
	for _, dir := range []string{"/usr/share/glvnd/egl_vendor.d", "/etc/glvnd/egl_vendor.d"} {
		mesa := dir + "/50_mesa.json"
		if _, err := os.Stat(mesa); err == nil {
			_ = os.Setenv("__EGL_VENDOR_LIBRARY_FILENAMES", mesa)
			return
		}
	}
}
