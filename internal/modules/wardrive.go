package modules

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/n0xa/czconsole/internal/wardrive"
)

//go:embed assets/wardrive
var wardriveUI embed.FS

// wardriveModule is the HTTP frontend for wardrive capture. All capture logic
// lives in the shared wardrive.Core (the native LCD wraps the same core), so the
// web and on-device views show identical state. This file is just request
// plumbing + the web-only export.
type wardriveModule struct {
	core *wardrive.Core
}

func NewWardriveModule() *wardriveModule {
	return &wardriveModule{core: wardrive.New()}
}

func (m *wardriveModule) Manifest() Manifest { return wardriveManifest() }

// TileStatus surfaces capture state on the dashboard tile: green "running",
// grey "stopped", amber "no adapter". (No red "failed" — a cgroup read can't
// distinguish a crashed kismet from a clean stop.)
func (m *wardriveModule) TileStatus() *TileStatus {
	s := m.core.Status()
	if s.Running {
		return &TileStatus{State: "running", Label: "running"}
	}
	if !s.AdapterPresent {
		return &TileStatus{State: "warn", Label: "no adapter"}
	}
	return &TileStatus{State: "stopped", Label: "stopped"}
}

func (m *wardriveModule) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	sub, _ := fs.Sub(wardriveUI, "assets/wardrive")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	mux.HandleFunc(prefix+"api/ifaces", auth(m.handleIfaces))
	mux.HandleFunc(prefix+"api/status", auth(m.handleStatus))
	mux.HandleFunc(prefix+"api/start", auth(m.handleStart))
	mux.HandleFunc(prefix+"api/stop", auth(m.handleStop))
	mux.HandleFunc(prefix+"api/export", auth(m.handleExport))
}

func (m *wardriveModule) handleIfaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ifaces": wardrive.Interfaces()})
}

func (m *wardriveModule) handleStatus(w http.ResponseWriter, r *http.Request) {
	s := m.core.Status()
	if !s.Running {
		writeJSON(w, map[string]any{"running": false, "adapter_present": s.AdapterPresent})
		return
	}
	st := map[string]any{"running": true, "iface": s.Iface, "uptime_s": s.UptimeSec}
	if s.StatsOK {
		st["stats_ok"] = true
		st["devices"] = s.Devices
		st["aps"] = s.APs
		st["clients"] = s.Clients
		st["new_per_min"] = s.NewPerMin
	} else {
		// Capturing (cgroup confirms live processes) but kismet's REST didn't
		// answer — report "stats unavailable", NOT "stopped".
		st["stats_ok"] = false
	}
	st["feed"] = m.core.RecentAPs(8)
	writeJSON(w, st)
}

func (m *wardriveModule) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// The core re-validates the interface against the live monitor-capable list
	// and refuses if a capture is already live (privsep: the privileged action
	// re-validates its inputs).
	iface := r.URL.Query().Get("iface")
	if err := m.core.Start(iface); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"running": true, "iface": iface})
}

func (m *wardriveModule) handleStop(w http.ResponseWriter, r *http.Request) {
	_ = m.core.Stop()
	writeJSON(w, map[string]any{"running": false})
}

// ── export (WiGLE CSV / KML) — web-only; reads the .kismet sqlite log ─────────

func (m *wardriveModule) handleExport(w http.ResponseWriter, r *http.Request) {
	fmtArg := r.URL.Query().Get("fmt")
	var tool, ext string
	switch fmtArg {
	case "wigle":
		tool, ext = "kismetdb_to_wiglecsv", "csv"
	case "kml":
		tool, ext = "kismetdb_to_kml", "kml"
	default:
		http.Error(w, "fmt must be wigle or kml", http.StatusBadRequest)
		return
	}
	// Capture logs live in the operator's ~/Wardriving (the worker sees it via a
	// read-only bind mount).
	logf := newestKismetLog(filepath.Join(DefaultFilesRoot(), "Wardriving"))
	if logf == "" {
		http.Error(w, "no capture log yet", http.StatusNotFound)
		return
	}
	tmp := filepath.Join(os.TempDir(), "czwardrive."+ext)
	defer os.Remove(tmp)
	// --skip-clean: don't VACUUM (needs an exclusive lock kismet holds while
	// capturing), so exports work mid-session against the live db.
	out, err := exec.Command(tool, "--in", logf, "--out", tmp, "--force", "--skip-clean").CombinedOutput()
	if err != nil {
		http.Error(w, tool+": "+err.Error()+"\n"+string(out), http.StatusInternalServerError)
		return
	}
	f, err := os.Open(tmp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="wardrive.%s"`, ext))
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, f)
}

// newestKismetLog returns the most recently modified *.kismet capture in dir.
func newestKismetLog(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.kismet"))
	var newest string
	var newestT time.Time
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newestT) {
			newest, newestT = p, fi.ModTime()
		}
	}
	return newest
}

// writeJSON is the package-wide JSON responder (also used by sibling modules).
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
