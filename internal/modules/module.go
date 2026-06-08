// Package modules is the framework core: it holds the available modules,
// resolves each one's availability against the live system, mounts their HTTP
// routes under /m/<id>/, and exposes a uniform Manifest the dashboard renders.
//
// A module is a Go value implementing Module. Bundled modules ship in the
// binary (Files is fully implemented; the recon/SDR/wardrive tiles are
// placeholders until their real modules land). External manifest modules
// (modules-dir) are discovered at Load time — schema finalized with wardrive.
package modules

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Middleware wraps a handler with the server's shared-token auth. Modules apply
// it to their API endpoints so a token (when configured) protects every route.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// Module is one capability. Manifest drives the dashboard tile + availability;
// Mount registers the module's routes (detail UI + API) under its prefix.
type Module interface {
	Manifest() Manifest
	Mount(mux *http.ServeMux, prefix string, auth Middleware)
}

// StatusProvider is an OPTIONAL interface a module implements to surface a live
// run-state on its dashboard tile (e.g. HDMI/wardrive). Describe() calls it on
// each /api/modules poll, so the implementation must be cheap and fork-free —
// the existing unitCgroupActive / dmEnabled file reads, never a systemctl fork
// (the dashboard polls even while the box is thrashing). Stateless modules
// (Files, placeholders) don't implement it and keep the static "ready" tag.
type StatusProvider interface {
	TileStatus() *TileStatus
}

// TileStatus is the live run-state rendered as a coloured dot on the tile.
// State is one of "running" (green), "stopped" (grey), "failed" (red),
// "unknown" (neutral). Label is the short word shown next to the dot.
type TileStatus struct {
	State string `json:"state"`
	Label string `json:"label"`
}

// Manifest is the declarative description the front-end renders generically.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Icon        string   `json:"icon"`
	Description string   `json:"description"`
	Requires    Requires `json:"requires"`
	Actions     []Action `json:"actions"`

	Available bool        `json:"available"`
	Missing   []string    `json:"missing,omitempty"`
	Source    string      `json:"source"`
	Status    *TileStatus `json:"status,omitempty"` // live run-state, set by Describe for StatusProviders
}

type Requires struct {
	Binaries     []string `json:"binaries,omitempty"`
	Files        []string `json:"files,omitempty"`
	AnyInterface string   `json:"any_interface,omitempty"`
}

type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type Registry struct {
	dir  string
	mods []Module
}

// NewRegistry builds the web worker's module set. filesSock is the unix socket
// of the files-agent process (the worker proxies Files to it, since the
// unprivileged worker can't write the operator's home itself).
func NewRegistry(dir, filesSock string) *Registry {
	return &Registry{
		dir: dir,
		mods: []Module{
			NewFilesProxy(filesSock),
			NewWardriveModule(),
			NewHDMIModule(),
			NewSDRModule(),
			newPlaceholder(netreconManifest()),
		},
	}
}

// Load scans modules-dir for external manifest modules. (Parsing lands with the
// wardrive module; for now bundled modules are already registered.)
func (r *Registry) Load() error {
	if _, err := os.Stat(r.dir); err != nil {
		return nil // absent modules-dir is fine
	}
	// TODO: parse module.toml entries and append wrapped Modules.
	return nil
}

// Describe returns every module with availability resolved, available-first.
func (r *Registry) Describe() []Manifest {
	out := make([]Manifest, 0, len(r.mods))
	for _, m := range r.mods {
		man := m.Manifest()
		man.Available, man.Missing = resolve(man.Requires)
		// Live run-state for the tile, only for available StatusProviders (no
		// point computing it for a module whose deps are missing). Fork-free.
		if man.Available {
			if sp, ok := m.(StatusProvider); ok {
				man.Status = sp.TileStatus()
			}
		}
		out = append(out, man)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Available != out[b].Available {
			return out[a].Available
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// Mount registers every module's routes under /m/<id>/, auth-wrapping APIs.
func (r *Registry) Mount(mux *http.ServeMux, auth Middleware) {
	for _, m := range r.mods {
		m.Mount(mux, "/m/"+m.Manifest().ID+"/", auth)
	}
}

// unitCgroupActive reports whether a systemd unit has live processes, read
// straight from its cgroup — a plain file read, NO fork. This matters on the
// memory-starved 512 MB CM0: shelling out to `systemctl is-active` forks a
// process that under swap thrash either ENOMEM-fails or hangs in D-state, and
// treating that as "stopped" misreports a live unit as dead (see wardrive.go).
// The parent slice path varies, so glob for the unit's (fixed) leaf cgroup dir:
// plain services sit at system.slice/<unit>/, but a templated dashed unit nests
// under an auto-generated slice (system.slice/system-foo\x2dbar.slice/…). The
// \x2d is literal in the dir name. Returns (active, known); known=false means
// genuinely indeterminate — callers must NOT downgrade that to "stopped".
func unitCgroupActive(unit string) (active, known bool) {
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

func resolve(req Requires) (ok bool, missing []string) {
	ok = true
	for _, bin := range req.Binaries {
		if _, err := exec.LookPath(bin); err != nil {
			ok = false
			missing = append(missing, "needs "+bin)
		}
	}
	for _, f := range req.Files {
		if _, err := os.Stat(f); err != nil {
			ok = false
			missing = append(missing, "needs "+f)
		}
	}
	return
}

// ── Placeholder manifests for not-yet-built modules (dashboard tiles only) ──

func wardriveManifest() Manifest {
	return Manifest{
		ID: "wardrive", Name: "Wardrive", Icon: "radar",
		Description: "Kismet + GPS site survey with WiGLE/KML export",
		Requires:    Requires{Binaries: []string{"kismet", "gpspipe"}, AnyInterface: "monitor"},
		Source:      "bundled",
	}
}

func sdrManifest() Manifest {
	return Manifest{
		ID: "sdr", Name: "SDR Sweep", Icon: "wave",
		Description: "rtl_power spectrum sweep + rtl_433 ISM decoder",
		Requires:    Requires{Binaries: []string{"rtl_power", "rtl_433", "rfheatmap"}},
		Source:      "bundled",
	}
}

func netreconManifest() Manifest {
	return Manifest{
		ID: "netrecon", Name: "Net Recon", Icon: "crosshair",
		Description: "nmap host/port discovery on the connected network",
		Requires:    Requires{Binaries: []string{"nmap"}},
		Source:      "bundled",
	}
}
