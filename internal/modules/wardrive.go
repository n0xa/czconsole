package modules

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed assets/wardrive
var wardriveUI embed.FS

// wardriveHome is the writable state/log/config root for kismet, owned by the
// worker user (created by setup-privsep.sh). kismet runs unprivileged here via
// its setcap'd capture helper + the `kismet` group — no root, no sudo.
const wardriveHome = "/var/lib/czconsole/wardrive"
const kismetREST = "http://localhost:2501"

type wardriveModule struct {
	mu      sync.Mutex
	iface   string    // non-empty while a capture unit is active
	started time.Time
	pass    string // per-run kismet REST password (generated, never static)
	hc      *http.Client
}

func unitFor(iface string) string { return "czconsole-kismet@" + iface + ".service" }

func NewWardriveModule() *wardriveModule {
	return &wardriveModule{hc: &http.Client{Timeout: 4 * time.Second}}
}

func (m *wardriveModule) Manifest() Manifest { return wardriveManifest() }

func (m *wardriveModule) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	sub, _ := fs.Sub(wardriveUI, "assets/wardrive")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	mux.HandleFunc(prefix+"api/ifaces", auth(m.handleIfaces))
	mux.HandleFunc(prefix+"api/status", auth(m.handleStatus))
	mux.HandleFunc(prefix+"api/start", auth(m.handleStart))
	mux.HandleFunc(prefix+"api/stop", auth(m.handleStop))
	mux.HandleFunc(prefix+"api/export", auth(m.handleExport))
}

// ── interface picker ──────────────────────────────────────────────────────

func wifiInterfaces() []string {
	var out []string
	nics, _ := os.ReadDir("/sys/class/net")
	for _, n := range nics {
		name := n.Name()
		if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func validIface(name string) bool {
	for _, w := range wifiInterfaces() {
		if w == name {
			return true
		}
	}
	return false
}

func (m *wardriveModule) handleIfaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ifaces": wifiInterfaces()})
}

// ── start / stop ──────────────────────────────────────────────────────────

func (m *wardriveModule) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	iface := r.URL.Query().Get("iface")
	// Validate against the live interface list — never exec an attacker-chosen
	// string (privsep rule: the privileged action re-validates its inputs).
	if !validIface(iface) {
		http.Error(w, "unknown wifi interface", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running() {
		http.Error(w, "already running on "+m.iface, http.StatusConflict)
		return
	}

	m.pass = randHex(16)
	if err := m.writeConfig(); err != nil {
		http.Error(w, "config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	os.MkdirAll(filepath.Join(wardriveHome, "logs"), 0o750)

	// Launch the scoped kismet unit via systemd (polkit-authorized). kismet's
	// privileges live in the unit, NOT in this worker — see czconsole-kismet@.service.
	// --no-block: queue the start and return immediately so a slow/hung kismet
	// startup never blocks this HTTP handler (it would otherwise wait up to the
	// unit's TimeoutStartSec). The status poll's is-active check reflects the
	// real state once kismet comes up (or fails).
	if out, err := exec.Command("systemctl", "start", "--no-block", unitFor(iface)).CombinedOutput(); err != nil {
		http.Error(w, "systemctl start: "+err.Error()+"\n"+string(out), http.StatusInternalServerError)
		return
	}
	m.iface = iface
	m.started = time.Now()
	writeJSON(w, map[string]any{"running": true, "iface": iface})
}

func (m *wardriveModule) handleStop(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.iface == "" {
		writeJSON(w, map[string]any{"running": false})
		return
	}
	// systemd SIGTERMs kismet, which flushes the .kismet log on clean shutdown.
	exec.Command("systemctl", "stop", unitFor(m.iface)).Run()
	m.iface = ""
	writeJSON(w, map[string]any{"running": false})
}

// running reports whether the capture unit is active. Caller holds mu.
func (m *wardriveModule) running() bool {
	if m.iface == "" {
		return false
	}
	// `systemctl is-active --quiet` exits 0 iff active.
	return exec.Command("systemctl", "is-active", "--quiet", unitFor(m.iface)).Run() == nil
}

// ── status ────────────────────────────────────────────────────────────────

func (m *wardriveModule) handleStatus(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	run := m.running()
	iface := m.iface
	started := m.started
	m.mu.Unlock()

	if !run {
		writeJSON(w, map[string]any{"running": false})
		return
	}

	st := map[string]any{"running": true, "iface": iface,
		"uptime_s": int(time.Since(started).Seconds())}

	if devices, ok := m.kismetDeviceCount(); ok {
		st["devices"] = devices
		aps := m.kismetAPCount()
		st["aps"] = aps
		if devices >= aps {
			st["clients"] = devices - aps // wifi non-AP ≈ clients
		}
		mins := time.Since(started).Minutes()
		if mins < 1 {
			mins = 1
		}
		st["new_per_min"] = int(float64(devices)/mins + 0.5)
	}
	st["feed"] = m.kismetRecentAPs(8)
	writeJSON(w, st)
}

func (m *wardriveModule) kismetDeviceCount() (int, bool) {
	var s struct {
		Count int `json:"kismet.system.devices.count"`
	}
	if err := m.restJSON("GET", "/system/status.json", nil, &s); err != nil {
		return 0, false
	}
	return s.Count, true
}

func (m *wardriveModule) kismetAPCount() int {
	var views []struct {
		ID   string `json:"kismet.devices.view.id"`
		Size int    `json:"kismet.devices.view.size"`
	}
	if err := m.restJSON("GET", "/devices/views/all_views.json", nil, &views); err != nil {
		return 0
	}
	for _, v := range views {
		if v.ID == "phydot11_accesspoints" {
			return v.Size
		}
	}
	return 0
}

type feedEntry struct {
	Name  string `json:"name"`
	Sig   int    `json:"sig"`
	Crypt string `json:"crypt"`
}

func (m *wardriveModule) kismetRecentAPs(n int) []feedEntry {
	// Kismet field-simplified POST with [path,rename] pairs → array of objects.
	spec := `{"fields":[` +
		`["kismet.device.base.commonname","name"],` +
		`["kismet.device.base.signal/kismet.common.signal.last_signal","sig"],` +
		`["kismet.device.base.crypt","crypt"],` +
		`["kismet.device.base.last_time","last"]]}`
	form := url.Values{"json": {spec}}
	var rows []struct {
		Name  string `json:"name"`
		Sig   int    `json:"sig"`
		Crypt string `json:"crypt"`
		Last  int64  `json:"last"`
	}
	if err := m.restJSON("POST", "/devices/views/phydot11_accesspoints/devices.json", form, &rows); err != nil {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Last > rows[j].Last })
	out := make([]feedEntry, 0, n)
	for i, row := range rows {
		if i >= n {
			break
		}
		name := row.Name
		if name == "" {
			name = "<hidden>"
		}
		out = append(out, feedEntry{Name: name, Sig: row.Sig, Crypt: row.Crypt})
	}
	return out
}

// ── export (WiGLE CSV / KML) ──────────────────────────────────────────────

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
	logf := latestKismetLog()
	if logf == "" {
		http.Error(w, "no capture log yet", http.StatusNotFound)
		return
	}
	tmp := filepath.Join(os.TempDir(), "czwardrive."+ext)
	defer os.Remove(tmp)
	// --skip-clean: don't VACUUM, which needs an exclusive lock kismet holds
	// while capturing. Lets exports run mid-session against the live db.
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

func latestKismetLog() string {
	matches, _ := filepath.Glob(filepath.Join(wardriveHome, "logs", "*.kismet"))
	var newest string
	var newestT time.Time
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newestT) {
			newest, newestT = p, fi.ModTime()
		}
	}
	return newest
}

// ── kismet config + REST helpers ──────────────────────────────────────────

func (m *wardriveModule) writeConfig() error {
	kdir := filepath.Join(wardriveHome, ".kismet")
	if err := os.MkdirAll(kdir, 0o750); err != nil {
		return err
	}
	httpd := fmt.Sprintf("httpd_username=czconsole\nhttpd_password=%s\n", m.pass)
	if err := os.WriteFile(filepath.Join(kdir, "kismet_httpd.conf"), []byte(httpd), 0o600); err != nil {
		return err
	}
	site := "gps=gpsd:host=localhost,port=2947\n"
	return os.WriteFile(filepath.Join(kdir, "kismet_site.conf"), []byte(site), 0o644)
}

func (m *wardriveModule) restJSON(method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, kismetREST+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth("czconsole", m.pass)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("kismet %s -> %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
