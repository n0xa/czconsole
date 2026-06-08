package modules

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed assets/sdr
var sdrUI embed.FS

const sdrRtlpowerUnit = "czconsole-rtlpower.service"
const sdrRtl433Unit = "czconsole-rtl433.service"
const sdrEnvDir = "/run/czconsole"

type sdrModule struct{ mu sync.Mutex }

func NewSDRModule() *sdrModule { return &sdrModule{} }

func (m *sdrModule) Manifest() Manifest { return sdrManifest() }

// rtlsdrPresent reports whether an RTL-SDR USB dongle is attached, read
// straight from sysfs — a plain file read, NO fork. RTL2832U-based dongles
// all use Realtek vendor 0x0bda with a small set of known product IDs that
// don't overlap with other Realtek USB devices (Ethernet 8152/8153, WiFi 8812).
func rtlsdrPresent() bool {
	sdrProducts := map[string]bool{
		"2832": true, // Generic RTL2832U
		"2838": true, // Ezcap / RTL-SDR Blog sticks (most common)
		"2820": true, // Xceive XC5000
		"2831": true, // RTL2831U
		"0bda": true, // Realtek RTL2832U (0bda:0bda)
	}
	vids, _ := filepath.Glob("/sys/bus/usb/devices/*/idVendor")
	for _, vidPath := range vids {
		v, err := os.ReadFile(vidPath)
		if err != nil || strings.TrimSpace(string(v)) != "0bda" {
			continue
		}
		p, err := os.ReadFile(filepath.Dir(vidPath) + "/idProduct")
		if err != nil {
			continue
		}
		if sdrProducts[strings.TrimSpace(string(p))] {
			return true
		}
	}
	return false
}

func (m *sdrModule) TileStatus() *TileStatus {
	pwrActive, _ := unitCgroupActive(sdrRtlpowerUnit)
	rdrActive, _ := unitCgroupActive(sdrRtl433Unit)
	if pwrActive || rdrActive {
		return &TileStatus{State: "running", Label: "running"}
	}
	if !rtlsdrPresent() {
		return &TileStatus{State: "warn", Label: "no device"}
	}
	return &TileStatus{State: "stopped", Label: "stopped"}
}

func (m *sdrModule) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	sub, _ := fs.Sub(sdrUI, "assets/sdr")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	mux.HandleFunc(prefix+"api/status", auth(m.handleStatus))
	mux.HandleFunc(prefix+"api/rtlpower/start", auth(m.handleRtlpowerStart))
	mux.HandleFunc(prefix+"api/rtlpower/stop", auth(m.handleRtlpowerStop))
	mux.HandleFunc(prefix+"api/rtl433/start", auth(m.handleRtl433Start))
	mux.HandleFunc(prefix+"api/rtl433/stop", auth(m.handleRtl433Stop))
}

func (m *sdrModule) handleStatus(w http.ResponseWriter, r *http.Request) {
	pwrActive, _ := unitCgroupActive(sdrRtlpowerUnit)
	rdrActive, _ := unitCgroupActive(sdrRtl433Unit)
	writeJSON(w, map[string]any{
		"device_present": rtlsdrPresent(),
		"rtlpower":       map[string]any{"running": pwrActive},
		"rtl433":         map[string]any{"running": rdrActive},
	})
}

// ── rtl_power ──────────────────────────────────────────────────────────────

type rtlPowerParams struct {
	LowMHz      int  `json:"low_mhz"`
	HighMHz     int  `json:"high_mhz"`
	BinKHz      int  `json:"bin_khz"`
	Crop        int  `json:"crop"`        // percent 0–90; converted to float for -c
	Gain        int  `json:"gain"`        // 0 = AGC
	Integration int  `json:"integration"` // seconds
	Duration    int  `json:"duration"`    // seconds; 0 = single-shot (-1 flag)
	Heatmap     bool `json:"heatmap"`
}

func defaultRtlPowerParams() rtlPowerParams {
	return rtlPowerParams{
		LowMHz: 450, HighMHz: 470, BinKHz: 5,
		Crop: 30, Gain: 20, Integration: 10,
		Duration: 300, Heatmap: true,
	}
}

func (p rtlPowerParams) validate() error {
	switch {
	case p.LowMHz < 27 || p.LowMHz > 1700:
		return fmt.Errorf("low_mhz must be 27–1700")
	case p.HighMHz < 27 || p.HighMHz > 1700:
		return fmt.Errorf("high_mhz must be 27–1700")
	case p.HighMHz < p.LowMHz:
		return fmt.Errorf("high_mhz must be ≥ low_mhz")
	case p.BinKHz < 1 || p.BinKHz > 1000:
		return fmt.Errorf("bin_khz must be 1–1000")
	case p.Crop < 0 || p.Crop > 90:
		return fmt.Errorf("crop must be 0–90")
	case p.Gain < 0 || p.Gain > 50:
		return fmt.Errorf("gain must be 0–50")
	case p.Integration < 1 || p.Integration > 600:
		return fmt.Errorf("integration must be 1–600 seconds")
	case p.Duration < 0 || p.Duration > 86400:
		return fmt.Errorf("duration must be 0–86400 seconds")
	}
	return nil
}

func (m *sdrModule) handleRtlpowerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	p := defaultRtlPowerParams()
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := p.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if active, _ := unitCgroupActive(sdrRtlpowerUnit); active {
		http.Error(w, "rtl_power already running", http.StatusConflict)
		return
	}
	if active, _ := unitCgroupActive(sdrRtl433Unit); active {
		http.Error(w, "rtl_433 is running — stop it first", http.StatusConflict)
		return
	}

	heatmap := "0"
	if p.Heatmap {
		heatmap = "1"
	}
	env := fmt.Sprintf(
		"SDR_LOW_MHZ=%d\nSDR_HIGH_MHZ=%d\nSDR_BIN_KHZ=%d\nSDR_CROP=%.2f\nSDR_GAIN=%d\nSDR_INTEGRATION=%d\nSDR_DURATION=%d\nSDR_HEATMAP=%s\n",
		p.LowMHz, p.HighMHz, p.BinKHz, float64(p.Crop)/100.0, p.Gain, p.Integration, p.Duration, heatmap,
	)
	if err := os.WriteFile(sdrEnvDir+"/sdr-rtlpower.env", []byte(env), 0o600); err != nil {
		http.Error(w, "env file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if out, err := exec.Command("systemctl", "start", "--no-block", sdrRtlpowerUnit).CombinedOutput(); err != nil {
		http.Error(w, "systemctl start: "+err.Error()+"\n"+string(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"running": true})
}

func (m *sdrModule) handleRtlpowerStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	exec.Command("systemctl", "stop", sdrRtlpowerUnit).Run()
	writeJSON(w, map[string]any{"running": false})
}

// ── rtl_433 ────────────────────────────────────────────────────────────────

type rtl433Params struct {
	FreqMHz  float64 `json:"freq_mhz"`
	BwKHz    int     `json:"bw_khz"`
	Gain     int     `json:"gain"`
	Duration int     `json:"duration"` // 0 = continuous (no timeout)
}

func defaultRtl433Params() rtl433Params {
	return rtl433Params{FreqMHz: 433.92, BwKHz: 250, Gain: 0, Duration: 300}
}

func (p rtl433Params) validate() error {
	switch {
	case p.FreqMHz < 27.0 || p.FreqMHz > 1700.0:
		return fmt.Errorf("freq_mhz must be 27–1700")
	case p.BwKHz < 1 || p.BwKHz > 1000:
		return fmt.Errorf("bw_khz must be 1–1000")
	case p.Gain < 0 || p.Gain > 50:
		return fmt.Errorf("gain must be 0–50")
	case p.Duration < 0 || p.Duration > 86400:
		return fmt.Errorf("duration must be 0–86400 seconds")
	}
	return nil
}

func (m *sdrModule) handleRtl433Start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	p := defaultRtl433Params()
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := p.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if active, _ := unitCgroupActive(sdrRtl433Unit); active {
		http.Error(w, "rtl_433 already running", http.StatusConflict)
		return
	}
	if active, _ := unitCgroupActive(sdrRtlpowerUnit); active {
		http.Error(w, "rtl_power is running — stop it first", http.StatusConflict)
		return
	}

	env := fmt.Sprintf(
		"SDR_FREQ_MHZ=%g\nSDR_BW_KHZ=%d\nSDR_GAIN=%d\nSDR_DURATION=%d\n",
		p.FreqMHz, p.BwKHz, p.Gain, p.Duration,
	)
	if err := os.WriteFile(sdrEnvDir+"/sdr-rtl433.env", []byte(env), 0o600); err != nil {
		http.Error(w, "env file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if out, err := exec.Command("systemctl", "start", "--no-block", sdrRtl433Unit).CombinedOutput(); err != nil {
		http.Error(w, "systemctl start: "+err.Error()+"\n"+string(out), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"running": true})
}

func (m *sdrModule) handleRtl433Stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	exec.Command("systemctl", "stop", sdrRtl433Unit).Run()
	writeJSON(w, map[string]any{"running": false})
}
