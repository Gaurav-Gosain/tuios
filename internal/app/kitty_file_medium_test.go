package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

const kittyMediumWin = "window-medium-0000-0000-000000000000"

// kittyFileCmd is an a=T of a 2x2 RGBA image read from a file medium.
func kittyFileCmd(medium vt.KittyGraphicsMedium, path string) *vt.KittyCommand {
	return &vt.KittyCommand{
		Action:   vt.KittyActionTransmitPlace,
		Medium:   medium,
		Format:   vt.KittyFormatRGBA,
		ImageID:  7,
		Width:    2,
		Height:   2,
		FilePath: path,
	}
}

// remoteKitty is a passthrough whose host is reached over ssh, so it cannot
// read this machine's files and tuios sends it the bytes.
func remoteKitty(t *testing.T) (*KittyPassthrough, *countingWriter) {
	t.Helper()
	withClientCaps(t, &HostCapabilities{KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20})
	host := &countingWriter{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, RemoteClient: true})
	if !kp.IsEnabled() {
		t.Fatal("passthrough not enabled")
	}
	return kp, host
}

// sendKitty forwards cmd and flushes whatever it queued to the host.
func sendKitty(kp *KittyPassthrough, cmd *vt.KittyCommand) {
	kp.ForwardCommand(cmd, nil, kittyMediumWin, 0, 0, 80, 24, 1, 1, 0, 0, 0, false, func([]byte) {})
	if data := kp.FlushPending(); len(data) > 0 {
		kp.WriteToHost(data)
	}
}

// secretFile writes 16 bytes, the size of a 2x2 RGBA image, to a file that
// stands for one the pane's output must not be able to name.
func secretFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("SECRETSECRETSECR"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A t=s name is a shared memory object in /dev/shm. A name holding ../ leaves
// it, and the file it reaches must not be read and sent to a remote viewer.
func TestKittyShmNameCannotLeaveDevShm(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("no /dev/shm")
	}
	kp, host := remoteKitty(t)
	secret := secretFile(t, t.TempDir(), "id_ed25519")

	sendKitty(kp, kittyFileCmd(vt.KittyMediumSharedMemory, "../.."+secret))

	if n := host.Total(); n != 0 {
		t.Fatalf("a t=s name with ../ sent %d bytes to the remote viewer", n)
	}
}

// homeSecret writes a secret file in a directory that is not a temporary
// one. The test process's own home is under /tmp, so the temporary
// directories are narrowed to one directory beside it for the test.
func homeSecret(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	tmp := filepath.Join(root, "tmp")
	home := filepath.Join(root, "home")
	for _, d := range []string{tmp, home} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prev := kittyTempDirs
	kittyTempDirs = func() []string { return []string{tmp} }
	t.Cleanup(func() { kittyTempDirs = prev })
	return secretFile(t, home, name)
}

// t=f names any file. A remote viewer is not sent the bytes of a file outside
// a temporary directory: the pane's output alone must not be a way to read a
// key from the home directory.
func TestKittyFileMediumOutsideTempIsNotReadForARemoteViewer(t *testing.T) {
	kp, host := remoteKitty(t)

	sendKitty(kp, kittyFileCmd(vt.KittyMediumFile, homeSecret(t, "id_ed25519")))

	if n := host.Total(); n != 0 {
		t.Fatalf("a t=f path in the home directory sent %d bytes to the remote viewer", n)
	}
}

// The kitty spec lets a terminal refuse sensitive places. /proc is one.
func TestKittyTempFileInProcIsRefused(t *testing.T) {
	kp, host := remoteKitty(t)

	sendKitty(kp, kittyFileCmd(vt.KittyMediumTempFile, "/proc/self/environ"))

	if n := host.Total(); n != 0 {
		t.Fatalf("a t=t path in /proc sent %d bytes to the remote viewer", n)
	}
}

// A t=t file outside a temporary directory is refused too.
func TestKittyTempFileOutsideTempDirIsRefused(t *testing.T) {
	kp, host := remoteKitty(t)

	sendKitty(kp, kittyFileCmd(vt.KittyMediumTempFile, homeSecret(t, "tty-graphics-protocol-secret")))

	if n := host.Total(); n != 0 {
		t.Fatalf("a t=t path outside a temp dir sent %d bytes to the remote viewer", n)
	}
}

// The positive control: a temporary file as kitten icat writes it still
// reaches a remote viewer.
func TestKittyTempFileAsIcatWritesItIsSent(t *testing.T) {
	kp, host := remoteKitty(t)
	dir, err := os.MkdirTemp("", "tuios-kitty-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := secretFile(t, dir, "tty-graphics-protocol-frame.rgba")

	sendKitty(kp, kittyFileCmd(vt.KittyMediumTempFile, path))

	if host.Total() == 0 {
		t.Fatal("a t=t temp file as icat writes it was not sent")
	}
}

// A host terminal that reads files itself is sent the name, not the bytes.
// A t=s name that leaves /dev/shm is not sent at all.
func TestKittyShmNameWithSlashIsNotForwarded(t *testing.T) {
	withClientCaps(t, &HostCapabilities{KittyGraphics: true, KittyFileTransfer: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20})
	host := &hostRecorder{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host})
	if !kp.hostReadsFiles() {
		t.Fatal("the host should read files in this test")
	}
	name := "../../etc/passwd"

	kp.ForwardCommand(kittyFileCmd(vt.KittyMediumSharedMemory, name), nil, kittyMediumWin, 0, 0, 80, 24, 1, 1, 0, 0, 0, false, func([]byte) {})
	if data := kp.FlushPending(); len(data) > 0 {
		kp.WriteToHost(data)
	}

	if strings.Contains(host.String(), base64.StdEncoding.EncodeToString([]byte(name))) {
		t.Fatal("a t=s name with ../ was forwarded to the host terminal")
	}
}
