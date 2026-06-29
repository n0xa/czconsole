// Package unit reports systemd unit liveness without forking — load-bearing on a
// memory-starved box where shelling out to `systemctl is-active` can fail with
// ENOMEM or hang in D-state under swap thrash.
package unit

import (
	"os"
	"path/filepath"
	"strings"
)

// CgroupActive reports whether a systemd unit has live processes, read straight
// from its cgroup — a plain file read, NO fork. Returns (active, known);
// known=false means we genuinely couldn't tell, so callers must NOT downgrade
// that to "stopped".
//
// The parent slice path varies: a plain service sits at system.slice/<unit>/,
// but a templated dashed unit nests under an auto-generated slice
// (system.slice/system-foo\x2dbar.slice/…), so glob for the unit's leaf dir.
func CgroupActive(unit string) (active, known bool) {
	globs := []string{
		"/sys/fs/cgroup/system.slice/" + unit + "/cgroup.procs",           // v2, no extra slice
		"/sys/fs/cgroup/system.slice/*/" + unit + "/cgroup.procs",         // v2, nested auto-slice
		"/sys/fs/cgroup/systemd/system.slice/" + unit + "/cgroup.procs",   // v1 hybrid
		"/sys/fs/cgroup/systemd/system.slice/*/" + unit + "/cgroup.procs", // v1 hybrid, nested
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, p := range matches {
			if b, err := os.ReadFile(p); err == nil {
				return len(strings.TrimSpace(string(b))) > 0, true
			}
		}
	}
	// systemd GCs a stopped unit's cgroup (and its empty auto-slice), so if the
	// hierarchy is mounted at all, no match ⇒ definitively not running.
	if _, err := os.Stat("/sys/fs/cgroup/system.slice"); err == nil {
		return false, true
	}
	return false, false
}
