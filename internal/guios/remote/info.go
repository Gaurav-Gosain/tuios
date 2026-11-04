package remote

import (
	"os"
	"os/user"
	"runtime"

	"github.com/Gaurav-Gosain/tuios/internal/guios/build"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/update"
)

// LocalInfo describes this machine to a remote client, which peer names.
func LocalInfo(peer string) proto.HostInfo {
	h := proto.HostInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: proto.Home(),
		Kero: build.Version(), Version: proto.Version, Server: build.Server, Peer: peer,
		Updates: build.Server && update.Managed(update.InstallDir())}
	h.Hostname, _ = os.Hostname()
	if u, err := user.Current(); err == nil {
		h.User = u.Username
	}
	return h
}
