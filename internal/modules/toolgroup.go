package modules

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"os/exec"
	"sort"
	"strings"

	"github.com/n0xa/czconsole/internal/tool"
)

//go:embed assets/tools
var toolsUI embed.FS

// toolGroup is a generic, spec-driven web module for one menu group (e.g. "Net
// Recon"): its dashboard tile, a group page listing the group's tools, and each
// tool's setup/results page — all rendered in the browser from the same
// /etc/czconsole/tools.d specs the LCD uses. Execution goes through tool.Runner
// (the polkit-started cap-class units); the browser reads output via the
// files-agent, so this network-facing worker never touches the operator home.
type toolGroup struct {
	id      string
	name    string
	icon    string
	specs   []tool.Spec
	runners map[string]*tool.Runner
	extras  []extraLink // nested bespoke modules (e.g. Wardrive), link-out cards
}

// extraLink is a bespoke module nested in this group's page — rendered as a card
// that links out to /m/<id> (its own UI) rather than a spec-driven tool page.
type extraLink struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Icon      string `json:"icon"`
	Available bool   `json:"available"`
}

// ToolGroups loads the tool specs and returns one module per group, in the menu's
// preferred order (Wireless, Net Recon, then the rest alphabetically).
func ToolGroups() []Module {
	specs, _ := tool.Load("")
	byGroup := map[string][]tool.Spec{}
	var groups []string
	for _, s := range specs {
		g := s.Group
		if g == "" {
			g = "Tools"
		}
		if _, ok := byGroup[g]; !ok {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], s)
	}
	orderGroups(groups)

	var mods []Module
	for _, g := range groups {
		tg := &toolGroup{
			id:      groupSlug(g),
			name:    g,
			icon:    groupIcon(g),
			specs:   byGroup[g],
			runners: map[string]*tool.Runner{},
		}
		for _, s := range tg.specs {
			tg.runners[s.ID] = tool.NewRunner(s)
		}
		mods = append(mods, tg)
	}
	return mods
}

func (g *toolGroup) Manifest() Manifest {
	// The group tile is always available; per-tool availability is shown on the
	// group page (a group shouldn't vanish because one tool's binary is missing).
	return Manifest{
		ID: g.id, Name: g.name, Icon: g.icon,
		Description: g.toolNames(),
		Source:      "bundled",
	}
}

func (g *toolGroup) toolNames() string {
	var n []string
	for _, s := range g.specs {
		n = append(n, s.Name)
	}
	for _, e := range g.extras {
		n = append(n, e.Name)
	}
	return strings.Join(n, " · ")
}

func (g *toolGroup) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	sub, _ := fs.Sub(toolsUI, "assets/tools")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	mux.HandleFunc(prefix+"api/specs", auth(g.handleSpecs))
	mux.HandleFunc(prefix+"api/status", auth(g.handleStatus))
	mux.HandleFunc(prefix+"api/start", auth(g.handleStart))
	mux.HandleFunc(prefix+"api/stop", auth(g.handleStop))
}

// toolInfo is a spec plus whether its binary is installed.
type toolInfo struct {
	tool.Spec
	Available bool `json:"available"`
}

func (g *toolGroup) handleSpecs(w http.ResponseWriter, r *http.Request) {
	tools := make([]toolInfo, 0, len(g.specs))
	for _, s := range g.specs {
		_, err := exec.LookPath(s.Binary)
		tools = append(tools, toolInfo{Spec: s, Available: err == nil})
	}
	writeJSON(w, map[string]any{"tools": tools, "extras": g.extras})
}

func (g *toolGroup) handleStatus(w http.ResponseWriter, r *http.Request) {
	rn := g.runners[r.URL.Query().Get("tool")]
	if rn == nil {
		http.Error(w, "unknown tool", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"running": rn.Running()})
}

func (g *toolGroup) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	rn := g.runners[r.URL.Query().Get("tool")]
	if rn == nil {
		http.Error(w, "unknown tool", http.StatusNotFound)
		return
	}
	var vals map[string]string
	if err := json.NewDecoder(r.Body).Decode(&vals); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := rn.Start(vals); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"running": true})
}

func (g *toolGroup) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	rn := g.runners[r.URL.Query().Get("tool")]
	if rn == nil {
		http.Error(w, "unknown tool", http.StatusNotFound)
		return
	}
	_ = rn.Stop()
	writeJSON(w, map[string]any{"running": false})
}

// ── group presentation helpers ───────────────────────────────────────────────

func groupSlug(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", ""))
}

func groupIcon(name string) string {
	switch name {
	case "Net Recon":
		return "crosshair"
	case "Wireless":
		return "wave"
	default:
		return "wrench"
	}
}

// orderGroups sorts in place: preferred groups first, then alphabetical (mirrors
// the LCD menu order).
func orderGroups(gs []string) {
	pref := map[string]int{"Wireless": 0, "Net Recon": 1}
	sort.Slice(gs, func(i, j int) bool {
		pi, oki := pref[gs[i]]
		pj, okj := pref[gs[j]]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki
		}
		return gs[i] < gs[j]
	})
}
