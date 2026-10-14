package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/adrg/xdg"
)

// A link to one server must not reach another server's state: the grants
// record, the saved sessions and the daemon log of every server are tuios's
// own files, whichever server the link arrived at. Before named servers the
// state deny entry was xdg.StateHome/tuios, and scoping xdg.StateHome moved it
// under tuios-servers/<name>, which would have opened the default server's
// folder to a link that reached a named one.
//
// The ways this can fail, written down first:
//   - the deny entry follows the scoped state home, so the default server's
//     folder is open to a named server's link;
//   - the tuios-servers folder is not denied, so a named server's folder is
//     open to the default server's link, or to another named server's;
//   - the read list and the write list disagree;
//   - the deny is so wide that an ordinary file beside them is refused.
func TestLinkDenyCoversEveryServersState(t *testing.T) {
	resetDenyKeysCache(t)
	base := serverscope.BaseStateHome()
	t.Cleanup(func() { _ = serverscope.Apply("") })
	// The isolated test tree points every XDG folder at one place, which would
	// let the config folder's deny entry stand in for the state folder's. Each
	// gets a folder of its own inside the tree here.
	config, data := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = filepath.Join(config, "config-home"), filepath.Join(data, "data-home")
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = config, data })

	own := []string{
		filepath.Join(base, "tuios", "sessions", "grants", "applied.json"),
		filepath.Join(base, serverscope.StateDirName, "foo", "tuios", "sessions", "grants", "applied.json"),
		filepath.Join(base, serverscope.StateDirName, "bar", "tuios", "daemon.log"),
	}
	ordinary := filepath.Join(base, "some-other-program", "history")

	for _, server := range []string{"", "foo"} {
		t.Run("server="+server, func(t *testing.T) {
			if err := serverscope.Apply(server); err != nil {
				t.Fatal(err)
			}
			for _, p := range own {
				rp := realPath(p)
				if !linkDenied(rp, "", linkPrivate{}) {
					t.Errorf("a link to server %q may write %s", server, p)
				}
				if !linkReadDenied(rp, "", linkPrivate{}) {
					t.Errorf("a link to server %q may read %s", server, p)
				}
			}
			rp := realPath(ordinary)
			if linkDenied(rp, "", linkPrivate{}) || linkReadDenied(rp, "", linkPrivate{}) {
				t.Errorf("a link to server %q is refused %s, which is not tuios's", server, ordinary)
			}
		})
	}
}

// A link to one server must not reach another server's sockets or its stash
// either. A named server's socket folder sits inside the default server's, so
// the deny entry for a named server is the default's whole folder, with the
// server's own drop folder, where a link may leave files, still open.
//
// The ways this can fail, written down first:
//   - the entry stays at the named server's own folder, so its link reaches
//     the default server's socket and every other named server's;
//   - the entry climbs, but the drop folder it keeps open is the default
//     server's, not this server's own;
//   - the climb also happens for the default server, which would deny the
//     folder above its socket folder.
func TestLinkDenyCoversEveryServersSockets(t *testing.T) {
	resetDenyKeysCache(t)
	base := filepath.Join(t.TempDir(), "tuios")
	t.Cleanup(func() { _ = serverscope.Apply("") })
	for _, dir := range []string{base, filepath.Join(base, "servers", "foo", "drop"), filepath.Join(base, "servers", "bar")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	others := []string{
		filepath.Join(base, "tuios.sock"),
		filepath.Join(base, "drop", "note.txt"),
		filepath.Join(base, "servers", "bar", "tuios.sock"),
		filepath.Join(base, "servers", "foo", "tuios.sock"),
	}
	ownDrop := filepath.Join(base, "servers", "foo", "drop", "note.txt")

	if err := serverscope.Apply("foo"); err != nil {
		t.Fatal(err)
	}
	lp := linkPrivateFor(filepath.Join(serverscope.SocketDir(base), "tuios.sock"))
	for _, p := range others {
		rp := realPath(p)
		if !linkDenied(rp, "", lp) || !linkReadDenied(rp, "", lp) {
			t.Errorf("a link to server foo may reach %s", p)
		}
	}
	if rp := realPath(ownDrop); linkDenied(rp, "", lp) || linkReadDenied(rp, "", lp) {
		t.Errorf("a link to server foo is refused its own drop folder: %s", ownDrop)
	}

	if err := serverscope.Apply(""); err != nil {
		t.Fatal(err)
	}
	lp = linkPrivateFor(filepath.Join(base, "tuios.sock"))
	if rp := realPath(filepath.Join(filepath.Dir(base), "elsewhere")); linkDenied(rp, "", lp) {
		t.Errorf("the default server's link is refused the folder beside its socket folder")
	}
}

// resetDenyKeysCache empties the prepared deny lists before and after a test
// that moves the folders they are built from, since a list is reused for a
// couple of seconds and its key does not name those folders.
func resetDenyKeysCache(t *testing.T) {
	reset := func() {
		denyKeysCache.Lock()
		denyKeysCache.m = nil
		denyKeysCache.Unlock()
	}
	reset()
	t.Cleanup(reset)
}
