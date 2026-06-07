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
	"sort"
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

// Manifest is the declarative description the front-end renders generically.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Icon        string   `json:"icon"`
	Description string   `json:"description"`
	Requires    Requires `json:"requires"`
	Actions     []Action `json:"actions"`

	Available bool     `json:"available"`
	Missing   []string `json:"missing,omitempty"`
	Source    string   `json:"source"`
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
			newPlaceholder(sdrManifest()),
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
		Requires:    Requires{Binaries: []string{"rtl_power", "rtl_433"}},
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
