package modules

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/n0xa/czconsole/internal/nmap"
)

//go:embed assets/netrecon
var netreconUI embed.FS

// nmapModule is the HTTP frontend for nmap scans. All scan logic lives in the
// shared nmap.Core (the native LCD wraps the same core), so the web and on-device
// views drive identical state. This file is just request plumbing; the web UI
// renders the most recent results below the scan form, the same shapes the LCD
// shows. Registered under the "netrecon" id (its dashboard tile).
type nmapModule struct{ core *nmap.Core }

func NewNmapModule() *nmapModule { return &nmapModule{core: nmap.New()} }

func (m *nmapModule) Manifest() Manifest { return netreconManifest() }

// TileStatus surfaces scan state on the dashboard tile.
func (m *nmapModule) TileStatus() *TileStatus {
	if m.core.Running() {
		return &TileStatus{State: "running", Label: "scanning"}
	}
	return &TileStatus{State: "stopped", Label: "idle"}
}

func (m *nmapModule) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	sub, _ := fs.Sub(netreconUI, "assets/netrecon")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	mux.HandleFunc(prefix+"api/status", auth(m.handleStatus))
	mux.HandleFunc(prefix+"api/start", auth(m.handleStart))
	mux.HandleFunc(prefix+"api/stop", auth(m.handleStop))
}

// handleStatus returns whether a scan is live + the latest parsed result (the
// same nmap.Result the LCD renders; JSON-tagged for the web view). result is
// null until the first scan completes.
func (m *nmapModule) handleStatus(w http.ResponseWriter, r *http.Request) {
	res, _ := m.core.LatestResult()
	running := m.core.Running()
	out := map[string]any{"running": running, "result": res}
	if running {
		out["options"] = m.core.RunningOpts() // what the live scan is scanning
	}
	writeJSON(w, out)
}

func (m *nmapModule) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Options   string `json:"options"`
		LogErrors bool   `json:"log_errors"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	// The core sanitizes options, refuses if a scan is already running, and runs
	// it via the operator unit (CAP_NET_RAW + --privileged) — same path as the LCD.
	if err := m.core.Start(body.Options, body.LogErrors); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"running": true})
}

func (m *nmapModule) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	_ = m.core.Stop()
	writeJSON(w, map[string]any{"running": false})
}
