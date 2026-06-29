// Package wardrive is the privilege-free orchestration core for wardrive capture:
// start/stop the per-interface kismet systemd unit, detect whether it's running
// (fork-free cgroup read), and pull live stats from kismet's REST API. Both
// frontends — the web module (HTTP) and the native LCD — wrap this one core, so
// they show identical state.
//
// It holds NO privilege itself: start/stop go through systemd+polkit (authorized
// for the _czconsole worker), and the REST creds live in the worker-owned
// wardrive home. Exporting it is a Go-visibility change, not a privilege one —
// the boundary stays in polkit + the unit's scoped caps.
package wardrive

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/n0xa/czconsole/internal/sysinfo"
	"github.com/n0xa/czconsole/internal/unit"
)

const (
	home       = "/var/lib/czconsole/wardrive"
	kismetREST = "http://localhost:2501"
	restUser   = "czconsole"
)

func unitFor(iface string) string { return "czconsole-kismet@" + iface + ".service" }

// Status is a snapshot of capture state + live stats.
type Status struct {
	Running        bool
	Iface          string
	AdapterPresent bool
	StatsOK        bool // kismet REST answered (running but loaded box may be false)
	Devices        int
	APs            int
	Clients        int
	NewPerMin      int
	UptimeSec      int
	GPSFix         bool
	Lat, Lon       float64
}

// Core is a wardrive controller. Safe for concurrent use.
type Core struct {
	mu    sync.Mutex
	iface string // last-known active capture iface (for stop targeting)
	hc    *http.Client
}

func New() *Core { return &Core{hc: &http.Client{Timeout: 4 * time.Second}} }

// Interfaces lists monitor-capable wifi NICs (czconsole's driver allowlist;
// excludes the onboard brcmfmac).
func Interfaces() []string {
	var out []string
	nics, _ := os.ReadDir("/sys/class/net")
	for _, n := range nics {
		if sysinfo.MonCapIface(n.Name()) {
			out = append(out, n.Name())
		}
	}
	sort.Strings(out)
	return out
}

func validIface(name string) bool {
	for _, w := range Interfaces() {
		if w == name {
			return true
		}
	}
	return false
}

// activeIface returns the capture iface currently live (cached first, then a
// scan), or "" if none. Caller holds mu.
func (c *Core) activeIface() string {
	if c.iface != "" {
		active, known := unit.CgroupActive(unitFor(c.iface))
		if !known {
			return c.iface // can't tell; don't claim a believed-live capture dead
		}
		if !active {
			c.iface = ""
		}
		return c.iface
	}
	for _, w := range Interfaces() {
		if active, known := unit.CgroupActive(unitFor(w)); known && active {
			c.iface = w
			return w
		}
	}
	return ""
}

// Status returns current capture state + live stats (from kismet REST).
func (c *Core) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	var s Status
	s.AdapterPresent = len(Interfaces()) > 0
	iface := c.activeIface()
	if iface == "" {
		return s
	}
	s.Running = true
	s.Iface = iface

	pass := readPass()
	if pass == "" {
		return s // running but creds missing → stats unavailable
	}

	var sys struct {
		Count int   `json:"kismet.system.devices.count"`
		Now   int64 `json:"kismet.system.timestamp.sec"`
		Start int64 `json:"kismet.system.timestamp.start_sec"`
	}
	if err := c.rest("GET", "/system/status.json", pass, nil, &sys); err != nil {
		return s // capturing but REST didn't answer → StatsOK stays false
	}
	s.StatsOK = true
	s.Devices = sys.Count
	// Precise, skew-free uptime from kismet's own clock (both fields same doc).
	if sys.Now > sys.Start && sys.Start > 0 {
		s.UptimeSec = int(sys.Now - sys.Start)
	}
	s.APs = c.apCount(pass)
	if s.Devices >= s.APs {
		s.Clients = s.Devices - s.APs // wifi non-AP ≈ clients
	}
	mins := float64(s.UptimeSec) / 60.0
	if mins < 1 {
		mins = 1
	}
	s.NewPerMin = int(float64(s.Devices)/mins + 0.5)
	c.gps(pass, &s)
	return s
}

func (c *Core) apCount(pass string) int {
	var views []struct {
		ID   string `json:"kismet.devices.view.id"`
		Size int    `json:"kismet.devices.view.size"`
	}
	if err := c.rest("GET", "/devices/views/all_views.json", pass, nil, &views); err != nil {
		return 0
	}
	for _, v := range views {
		if v.ID == "phydot11_accesspoints" {
			return v.Size
		}
	}
	return 0
}

func (c *Core) gps(pass string, s *Status) {
	var loc struct {
		Geo []float64 `json:"kismet.common.location.geopoint"` // [lon, lat]; [0,0]=no fix
	}
	if err := c.rest("GET", "/gps/location.json", pass, nil, &loc); err != nil {
		return
	}
	if len(loc.Geo) == 2 && (loc.Geo[0] != 0 || loc.Geo[1] != 0) {
		s.GPSFix = true
		s.Lon = loc.Geo[0]
		s.Lat = loc.Geo[1]
	}
}

// Start launches the kismet unit for iface (only if nothing is capturing),
// ensuring a stable REST password exists first. Re-validates the interface — the
// privileged action never execs an attacker-chosen string.
func (c *Core) Start(iface string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validIface(iface) {
		return fmt.Errorf("unknown wifi interface: %s", iface)
	}
	if c.activeIface() != "" {
		return fmt.Errorf("already running on %s", c.iface)
	}
	if err := ensureConfig(); err != nil {
		return err
	}
	// Capture logs go to the operator's ~/Wardriving (the unit's -p), created by
	// the package; nothing for the Core to make here.
	if out, err := exec.Command("systemctl", "start", "--no-block", unitFor(iface)).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl start: %v: %s", err, strings.TrimSpace(string(out)))
	}
	c.iface = iface
	return nil
}

// Stop stops whichever capture unit is live.
func (c *Core) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	iface := c.activeIface()
	if iface == "" {
		return nil
	}
	err := exec.Command("systemctl", "stop", unitFor(iface)).Run()
	c.iface = ""
	return err
}

// Password returns the kismet REST password (for the LCD reveal key).
func (c *Core) Password() string { return readPass() }

// FeedEntry is one recently-seen access point (for the live feed).
type FeedEntry struct {
	Name  string `json:"name"`
	Sig   int    `json:"sig"`
	Crypt string `json:"crypt"`
}

// RecentAPs returns up to n most-recently-seen access points.
func (c *Core) RecentAPs(n int) []FeedEntry {
	pass := readPass()
	if pass == "" {
		return nil
	}
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
	if err := c.rest("POST", "/devices/views/phydot11_accesspoints/devices.json", pass, form, &rows); err != nil {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Last > rows[j].Last })
	out := make([]FeedEntry, 0, n)
	for i, row := range rows {
		if i >= n {
			break
		}
		name := row.Name
		if name == "" {
			name = "<hidden>"
		}
		out = append(out, FeedEntry{Name: name, Sig: row.Sig, Crypt: row.Crypt})
	}
	return out
}

func (c *Core) rest(method, path, pass string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, kismetREST+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(restUser, pass)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("kismet %s -> %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── kismet config / creds (STATIC password: generated once, then reused) ──────

func confPath() string { return filepath.Join(home, ".kismet", "kismet_httpd.conf") }

func readPass() string {
	b, err := os.ReadFile(confPath())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "httpd_password="); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// ensureConfig makes sure kismet's httpd + site config exist. The password is
// STATIC: generated once on first capture and reused thereafter, so it's stable
// (showable on the LCD, won't churn mid-session, identical across frontends).
func ensureConfig() error {
	kdir := filepath.Join(home, ".kismet")
	if err := os.MkdirAll(kdir, 0o750); err != nil {
		return err
	}
	if readPass() == "" {
		httpd := fmt.Sprintf("httpd_username=%s\nhttpd_password=%s\n", restUser, randHex(16))
		if err := os.WriteFile(confPath(), []byte(httpd), 0o600); err != nil {
			return err
		}
	}
	site := "gps=gpsd:host=localhost,port=2947\n"
	return os.WriteFile(filepath.Join(kdir, "kismet_site.conf"), []byte(site), 0o644)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
