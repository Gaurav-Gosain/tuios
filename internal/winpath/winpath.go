// Package winpath turns the POSIX paths that MSYS2 and Cygwin shells report
// into the Windows paths the rest of tuios reads.
//
// A shell under MSYS2 announces its folder as /c/Users/x, or as /home/x for a
// folder inside the MSYS2 tree. A native Windows program reads the first as
// \c\Users\x, which is no folder at all, so the rail listed nothing and said
// the folder was gone (issue #413). The drive form is converted on its own.
// The rest needs the folder MSYS2 is installed in, which is found once from
// the environment and kept.
//
// The file:// URL of an OSC 7 report is read here too (FileURL), because a
// native Windows shell's URL keeps a slash in front of the drive, /C:/x, and
// that is the same kind of path.
package winpath

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Mounts is what a root-relative POSIX path maps onto.
type Mounts struct {
	// Root is the Windows folder that / stands for, such as C:\msys64.
	// Empty when it is not known.
	Root string
	// Tmp is the Windows folder that /tmp stands for. MSYS2 mounts /tmp on
	// the user's temp folder. Empty means /tmp is an ordinary folder under
	// Root, as it is in Cygwin.
	Tmp string
}

// FromPOSIX converts p to a Windows path. It reports false when p is not an
// absolute path, or names a place under / and m has no root.
//
// The rules are cygpath's: /c/x and /cygdrive/c/x are on drive C, //host/share
// is a UNC path, /tmp is m.Tmp when set, and anything else is under m.Root. A
// path that is already a Windows path, in either slash, is returned in its
// clean form, which covers the /C:/x that a file:// URL from a native shell
// parses to.
func FromPOSIX(p string, m Mounts) (string, bool) {
	if p == "" {
		return "", false
	}
	if drive, rest, ok := windowsDrive(strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "/")); ok {
		return onDrive(drive, rest), true
	}
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		return unc(p)
	}
	if !strings.HasPrefix(p, "/") {
		return "", false
	}
	p = path.Clean(p)

	if rest, ok := cutDir(p, "/cygdrive"); ok {
		letter, tail, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		if !isLetter(letter) {
			return "", false
		}
		return onDrive(letter, tail), true
	}
	if letter, tail, _ := strings.Cut(p[1:], "/"); isLetter(letter) {
		return onDrive(letter, tail), true
	}
	if rest, ok := cutDir(p, "/tmp"); ok && m.Tmp != "" {
		return under(m.Tmp, rest), true
	}
	if m.Root == "" {
		return "", false
	}
	return under(m.Root, p), true
}

// windowsDrive splits "C:" or "C:/rest" into the drive letter and the rest.
func windowsDrive(p string) (drive, rest string, ok bool) {
	if len(p) < 2 || p[1] != ':' || !isLetter(p[:1]) {
		return "", "", false
	}
	if len(p) > 2 && p[2] != '/' {
		// C:x is relative to the current folder on drive C.
		return "", "", false
	}
	return p[:1], strings.TrimPrefix(p[2:], "/"), true
}

// onDrive is rest on the drive, cleaned, never above the drive's root.
func onDrive(letter, rest string) string {
	clean := strings.TrimPrefix(path.Clean("/"+rest), "/")
	return strings.ToUpper(letter) + `:\` + strings.ReplaceAll(clean, "/", `\`)
}

// unc converts //host/share/rest. Both the host and the share are required.
func unc(p string) (string, bool) {
	host, rest, _ := strings.Cut(p[2:], "/")
	share, tail, _ := strings.Cut(rest, "/")
	if host == "" || share == "" {
		return "", false
	}
	out := `\\` + host + `\` + share
	if clean := strings.TrimPrefix(path.Clean("/"+tail), "/"); clean != "" {
		out += `\` + strings.ReplaceAll(clean, "/", `\`)
	}
	return out, true
}

// under joins the clean POSIX path rest onto the Windows folder base.
func under(base, rest string) string {
	base = strings.TrimRight(base, `\/`)
	if len(base) == 2 && base[1] == ':' {
		base += `\`
	}
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return base
	}
	if !strings.HasSuffix(base, `\`) {
		base += `\`
	}
	return base + strings.ReplaceAll(rest, "/", `\`)
}

// cutDir reports whether p is dir or inside it, and returns the part after it.
func cutDir(p, dir string) (string, bool) {
	if p == dir {
		return "", true
	}
	if rest, ok := strings.CutPrefix(p, dir+"/"); ok {
		return "/" + rest, true
	}
	return "", false
}

// isLetter reports whether s is one ASCII letter.
func isLetter(s string) bool {
	return len(s) == 1 && (s[0]|0x20) >= 'a' && (s[0]|0x20) <= 'z'
}

// FindMounts finds the MSYS2, Git for Windows or Cygwin tree this process
// runs under, from the PATH value pathList and the EXEPATH value exepath.
// exists reports whether a file is there, and tmp is the user's temp folder.
//
// A shell from one of those trees hands a native program a PATH with the
// tree's bin folder in it. The runtime DLL in that folder proves which tree it
// is: msys-2.0.dll in usr\bin for MSYS2 and Git for Windows, cygwin1.dll in
// bin for Cygwin. Git Bash also sets EXEPATH to its root.
func FindMounts(pathList, exepath, tmp string, exists func(string) bool) Mounts {
	for _, entry := range strings.Split(pathList, ";") {
		entry = strings.TrimRight(strings.ReplaceAll(entry, "/", `\`), `\`)
		lower := strings.ToLower(entry)
		switch {
		case strings.HasSuffix(lower, `\usr\bin`) && exists(entry+`\msys-2.0.dll`):
			return Mounts{Root: entry[:len(entry)-len(`\usr\bin`)], Tmp: tmp}
		case strings.HasSuffix(lower, `\bin`) && exists(entry+`\cygwin1.dll`):
			return Mounts{Root: entry[:len(entry)-len(`\bin`)]}
		}
	}
	if exepath = strings.TrimRight(exepath, `\/`); exepath != "" && exists(exepath+`\usr\bin\msys-2.0.dll`) {
		return Mounts{Root: exepath, Tmp: tmp}
	}
	return Mounts{}
}

// FileURL splits a file:// URL that a shell reported over OSC 7 into the host
// it names and the folder, percent-decoded and in forward slashes. It is the
// one place tuios reads such a URL, on the client and in the daemon.
//
// The path of a URL from a native Windows shell keeps a slash in front of the
// drive, file://HOST/C:/x, and that slash is dropped here, so the folder is
// C:/x on every OS. A Windows host can report through ssh to a machine that
// is not Windows, and the folder is still shown by its own name there.
//
// The host is the machine the shell runs on, as OSC 7 means it, and never a
// file server: file://server/share/x is the folder /share/x on the machine
// "server". A shell in a UNC folder reports file://HOST//server/share/x, and
// the path keeps both of its leading slashes.
func FileURL(raw string) (host, p string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Path == "" {
		return "", "", false
	}
	// The path is decoded, so %00 or %1b is a control character now. No
	// folder name needs one, and the folder is shown on the rail and handed
	// to commands, so a report that carries one is refused.
	if strings.ContainsFunc(u.Path, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }) {
		return "", "", false
	}
	return u.Hostname(), dropDriveSlash(u.Path), true
}

// dropDriveSlash turns /C: and /C:/x into C: and C:/x. Any other path, /C:x
// among them, is returned unchanged: that is a folder named C:x.
func dropDriveSlash(p string) string {
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' && isLetter(p[1:2]) &&
		(len(p) == 3 || p[3] == '/' || p[3] == '\\') {
		return p[1:]
	}
	return p
}

// ForOS converts a folder a shell on this machine reported, from an OSC 7
// URL (after FileURL) or from OSC 9;9, into a clean absolute path for a
// machine that runs goos. It reports false when p is not one.
//
// On Windows, a drive path in either slash, a UNC path, and an MSYS2 or Cygwin
// path become a Windows path. An MSYS2 path under / needs the folder MSYS2 is
// installed in, which is looked up only when the process runs on Windows
// itself. On every other OS a drive path is refused, because no folder on this
// machine has that name.
func ForOS(p, goos string) (string, bool) {
	if goos != "windows" {
		if !strings.HasPrefix(p, "/") {
			return "", false
		}
		return path.Clean(p), true
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if w, ok := FromPOSIX(p, Mounts{}); ok {
		// A drive path, a UNC path or a Windows path: no root needed, so the
		// environment is not searched for one.
		return w, true
	}
	if runtime.GOOS != "windows" {
		return "", false
	}
	return FromPOSIX(p, localMounts())
}

// localMounts is the tree this process runs under, found on first use and
// kept. cygpath is asked only when the environment says nothing, and only
// once.
var localMounts = sync.OnceValue(func() Mounts {
	exists := func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && !info.IsDir()
	}
	m := FindMounts(os.Getenv("PATH"), os.Getenv("EXEPATH"), os.TempDir(), exists)
	if m.Root != "" {
		return m
	}
	return Mounts{Root: cygpathRoot()}
})

// cygpathRoot asks cygpath where / is, or returns "" when it cannot.
func cygpathRoot() string {
	bin, err := exec.LookPath("cygpath")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-w", "/").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(string(out)), `\`)
}
