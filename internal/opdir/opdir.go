// Package opdir resolves the operator's home and the per-tool output dirs the
// recon tools write to (~/<tool>/). Both deprivileged frontends read those dirs
// — the web worker via a read-only bind mount, the LCD via a czconsole-group
// traverse ACL on the home (see docs/native-lcd.md, "Two privileged subsystems").
//
// Kept in its own low-level package so every core (nmap, gobuster, tcpdump, …)
// shares one implementation without importing the modules package (which wraps
// them — that would be an import cycle).
package opdir

import (
	"os"
	"path/filepath"
)

// Home is the operator's home dir, matching modules.DefaultFilesRoot: kali,
// then pi, then the first /home/* dir, then $HOME, then /root.
func Home() string {
	for _, u := range []string{"kali", "pi"} {
		if fi, err := os.Stat("/home/" + u); err == nil && fi.IsDir() {
			return "/home/" + u
		}
	}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				return "/home/" + e.Name()
			}
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/root"
}

// Tool returns the operator's ~/<name> output dir (created setgid-czconsole by
// the package postinstall).
func Tool(name string) string { return filepath.Join(Home(), name) }
