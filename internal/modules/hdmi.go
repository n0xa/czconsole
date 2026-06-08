package modules

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
)

//go:embed assets/hdmi
var hdmiUI embed.FS

// dmUnit is the display-manager unit we control. lightdm on both the Kali graft
// and stock Raspberry Pi OS (Pixel) — it's what draws the HDMI desktop. On the
// 512 MB CM0 that desktop (Xorg + pipewire + wireplumber + the session) is the
// single biggest memory consumer and the first thing the OOM killer takes under
// load, so being able to drop it for a long wardrive run is the point of this
// module. The capture path (APPLaunch on the LCD framebuffer, czconsole, kismet,
// gpsd) is all headless and unaffected.
const dmUnit = "lightdm.service"

// The worker can't enable/disable lightdm itself (SysV init script ⇒ update-rc.d
// is a root filesystem write polkit can't grant). The privileged work lives in
// these two fixed-verb root oneshots; the worker only START them, authorized by
// 55-czconsole-hdmi.rules. enable --now / disable --now each cover one button
// (persist across reboot AND apply now) atomically inside the helper.
const (
	enableHelper  = "czconsole-hdmi-enable.service"
	disableHelper = "czconsole-hdmi-disable.service"
)

type hdmiModule struct{}

func NewHDMIModule() *hdmiModule { return &hdmiModule{} }

func hdmiManifest() Manifest {
	return Manifest{
		ID: "hdmi", Name: "HDMI Desktop", Icon: "monitor",
		Description: "Start/stop the lightdm HDMI desktop to free RAM",
		// Gate on lightdm being installed. /lib is the usr-merge symlink so this
		// resolves on both the graft and stock RaspiOS (both Trixie).
		Requires: Requires{Files: []string{"/lib/systemd/system/" + dmUnit}},
		Source:   "bundled",
	}
}

func (m *hdmiModule) Manifest() Manifest { return hdmiManifest() }

func (m *hdmiModule) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	sub, _ := fs.Sub(hdmiUI, "assets/hdmi")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	mux.HandleFunc(prefix+"api/status", auth(m.handleStatus))
	mux.HandleFunc(prefix+"api/enable", auth(m.handleEnable))
	mux.HandleFunc(prefix+"api/disable", auth(m.handleDisable))
}

// dmEnabled reports whether a display manager is enabled at boot, fork-free.
// `systemctl enable lightdm` installs the display-manager.service alias symlink
// (and a graphical.target.wants link on some setups); either being present is
// the boot-time enable marker. We only need this to tell "deliberately off"
// (disabled) from "should be up but isn't" (enabled) when the unit is inactive.
func dmEnabled() bool {
	for _, p := range []string{
		"/etc/systemd/system/display-manager.service",
		"/etc/systemd/system/graphical.target.wants/" + dmUnit,
	} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// state maps to the three UI colours the operator asked for:
//
//	running  → green  (desktop is up)
//	stopped  → grey   (deliberately off: inactive AND not enabled)
//	degraded → red    (enabled / should be up, but no live processes — e.g. the
//	                   OOM killer took it, or it failed to start)
// computeState is the single source of truth for HDMI run-state, shared by the
// detail endpoint and the dashboard tile so they can't drift. Fork-free:
// active=cgroup read (mechanics), enabled=boot symlink (persisted intent).
func (m *hdmiModule) computeState() (state string, active, enabled bool) {
	var known bool
	active, known = unitCgroupActive(dmUnit)
	enabled = dmEnabled()

	state = "stopped"
	switch {
	case active:
		state = "running"
	case !known:
		// Couldn't read the cgroup at all (shouldn't normally happen). Don't lie
		// either way — report unknown so the UI shows neutral, not a false green.
		state = "unknown"
	case enabled:
		state = "degraded" // enabled but no live procs
	}
	return
}

func (m *hdmiModule) handleStatus(w http.ResponseWriter, r *http.Request) {
	state, active, enabled := m.computeState()
	writeJSON(w, map[string]any{
		"state": state, "active": active, "enabled": enabled, "unit": dmUnit,
	})
}

// TileStatus maps the run-state to the dashboard tile's coloured dot. HDMI has
// the full green/grey/red because its enabled flag is a true intent signal:
// "degraded" (enabled but down) becomes the red "failed".
func (m *hdmiModule) TileStatus() *TileStatus {
	switch state, _, _ := m.computeState(); state {
	case "running":
		return &TileStatus{State: "running", Label: "running"}
	case "degraded":
		return &TileStatus{State: "failed", Label: "failed"}
	case "unknown":
		return &TileStatus{State: "unknown", Label: "?"}
	default:
		return &TileStatus{State: "stopped", Label: "stopped"}
	}
}

// handleEnable enables lightdm at boot AND starts it now, by triggering the root
// helper oneshot (Type=oneshot, so the start call blocks until it finishes and
// surfaces its exit status). Both effects are atomic inside the helper, so intent
// (the enabled boot symlink) and mechanics (running now) never disagree halfway.
func (m *hdmiModule) handleEnable(w http.ResponseWriter, r *http.Request) {
	m.toggle(w, r, enableHelper)
}

// handleDisable disables lightdm at boot AND stops it now, via the root helper —
// freeing the RAM the HDMI desktop holds.
func (m *hdmiModule) handleDisable(w http.ResponseWriter, r *http.Request) {
	m.toggle(w, r, disableHelper)
}

func (m *hdmiModule) toggle(w http.ResponseWriter, r *http.Request, helper string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// `systemctl start <oneshot>` returns non-zero if the helper's ExecStart
	// fails; its own output isn't echoed, so point at the journal for detail.
	if out, err := runSystemctl("start", helper); err != nil {
		http.Error(w, err.Error()+"\n"+out+"(detail: journalctl -u "+helper+")",
			http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// runSystemctl runs a systemctl verb on a unit (the HDMI helper units are
// polkit-authorized for _czconsole by 55-czconsole-hdmi.rules) and returns
// combined output for error reporting.
func runSystemctl(verb, unit string) (string, error) {
	out, err := exec.Command("systemctl", verb, unit).CombinedOutput()
	return string(out), err
}
