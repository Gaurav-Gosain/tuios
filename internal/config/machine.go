package config

import "os"

// ThisMachine is the name this machine signs a request with when the far side
// is another machine: TUIOS_HOST inside a pane, which the daemon set from the
// hostname, else the hostname. It is a claim the far side cannot check, and it
// is shown there as one.
func ThisMachine() string {
	if h := os.Getenv("TUIOS_HOST"); h != "" {
		return h
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// HostReposRoot is [hosts.NAME] repos_root from this machine's config, or ""
// when it is not set or the config cannot be read.
func HostReposRoot(host string) string {
	path, err := GetConfigPath()
	if err != nil {
		return ""
	}
	hosts, err := HostsInFile(path)
	if err != nil {
		return ""
	}
	return hosts[host].ReposRoot
}
