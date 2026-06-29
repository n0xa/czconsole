// Package modules is the framework core: it holds the available modules,
// resolves each one's availability against the live system, mounts their HTTP
// routes under /m/<id>/, and exposes a uniform Manifest the dashboard renders.
//
// A module is a Go value implementing Module. Bundled modules ship in the
// binary: Files (operator-home browser, proxied to the files-agent), HDMI, and
// Wardrive are bespoke; the recon/SDR tools are generic spec-driven group
// modules (Net Recon, Wireless) built from /etc/czconsole/tools.d. External
// manifest modules (modules-dir) are discovered at Load time.
package modules

import (
	"net/http"
	"os"
	"os/exec"
	"sort"

	"github.com/n0xa/czconsole/internal/unit"
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

	// Group, if set, nests this module inside a tool group's page (e.g. Wardrive
	// → "Wireless") instead of giving it a top-level dashboard tile. Still mounted.
	Group string `json:"group,omitempty"`
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
	bespoke := []Module{
		NewFilesProxy(filesSock),
		NewWardriveModule(),
		NewHDMIModule(),
	}
	// The recon/SDR tools are spec-driven group modules (Net Recon, Wireless),
	// built from /etc/czconsole/tools.d — same specs the LCD uses. (Replaces the
	// old per-tool nmap/sdr web modules.)
	groups := ToolGroups()
	// Nest grouped bespoke modules (e.g. Wardrive → Wireless) as link cards on
	// their group's page, mirroring how the LCD menu appends Wardrive to Wireless.
	for _, b := range bespoke {
		man := b.Manifest()
		if man.Group == "" {
			continue
		}
		avail, _ := resolve(man.Requires)
		for _, g := range groups {
			if tg, ok := g.(*toolGroup); ok && tg.name == man.Group {
				tg.extras = append(tg.extras, extraLink{Name: man.Name, ID: man.ID, Icon: man.Icon, Available: avail})
			}
		}
	}
	return &Registry{dir: dir, mods: append(bespoke, groups...)}
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
		if man.Group != "" {
			continue // nested inside its group's page, not a top-level tile
		}
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
// straight from its cgroup — a plain file read, NO fork. Thin wrapper over the
// shared internal/unit helper (kept so sdr/hdmi/wardrive callers are unchanged).
func unitCgroupActive(name string) (active, known bool) {
	return unit.CgroupActive(name)
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

func wardriveManifest() Manifest {
	return Manifest{
		ID: "wardrive", Name: "Wardrive", Icon: "radar",
		Description: "Kismet + GPS site survey with WiGLE/KML export",
		Requires:    Requires{Binaries: []string{"kismet", "gpspipe"}, AnyInterface: "monitor"},
		Source:      "bundled",
		Group:       "Wireless", // nested in the Wireless group page (mirrors the LCD menu)
	}
}
