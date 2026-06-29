// Package sysinfo gathers portable device telemetry that exists on any
// Pi-class Linux: CPU load, memory, SoC temperature, battery, and a quick
// inventory of network adapters with their monitor-mode capability. Battery
// reading is multi-source so it works whether or not a bq27xxx driver is bound
// (mirrors the fallback logic in APPLaunch's HAL).
package sysinfo

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/n0xa/czconsole/internal/gps"
)

type Info struct {
	Hostname   string    `json:"hostname"`
	Uptime     float64   `json:"uptime_s"`
	Load1      float64   `json:"load1"`
	MemTotalMB int       `json:"mem_total_mb"`
	MemAvailMB int       `json:"mem_avail_mb"`
	TempC      float64   `json:"temp_c"`
	Battery    Battery   `json:"battery"`
	GPS        gps.Fix   `json:"gps"`
	Adapters   []Adapter `json:"adapters"`
}

type Battery struct {
	Source    string `json:"source"`     // "sysfs", "i2c-bq27220", or "unknown"
	Percent   int    `json:"percent"`    // voltage-derived when from i2c
	VoltageMV int    `json:"voltage_mv"`
	Charging  bool   `json:"charging"`
	Valid     bool   `json:"valid"`
}

type Adapter struct {
	Name    string `json:"name"`
	Type    string `json:"type"`    // "wifi", "ethernet", "other"
	Driver  string `json:"driver,omitempty"`
	Monitor bool   `json:"monitor"` // actively in monitor mode (ARPHRD type 803)
	MonCap  bool   `json:"mon_cap"` // driver supports monitor mode (driver-inferred)
	Inject  bool   `json:"inject"`  // driver supports frame injection (driver-inferred)
	Up      bool   `json:"up"`
}

// driverCaps maps a Linux Wi-Fi driver to its monitor / injection support, the
// way the aircrack-ng community catalogs it (injection is a *driver* property,
// not an advertised kernel flag — the only authoritative test is
// `aireplay-ng --test`, which transmits). These flags are therefore
// driver-INFERRED, conservatively blank for unknown drivers. `inject` implies
// monitor. Covers the common pentest adapters + the CZ's onboard radio.
var driverCaps = map[string]struct{ mon, inject bool }{
	// Atheros — the gold standard
	"ath9k":     {true, true},
	"ath9k_htc": {true, true}, // AR9271 (TL-WN722N v1, AWUS036NHA)
	"carl9170":  {true, true},
	// Ralink / MediaTek
	"rt2800usb": {true, true}, // RT5370/5372/5572/3070 (Panda PAU05/09)
	"rt2800pci": {true, true},
	"rt73usb":   {true, true},
	"mt76x0u":   {true, true},
	"mt76x2u":   {true, true}, // MT7612U (AWUS036ACM)
	"mt76x2e":   {true, true},
	"mt7921u":   {true, true}, // MT7921 (AX adapters, AX3000)
	"mt7921e":   {true, true},
	"mt7921au":  {true, true},
	// Realtek out-of-tree (when built — see graft's 88XXau ask)
	"8812au":    {true, true}, // RTL8812AU/8811AU (AWUS036ACS/AC, Archer T2U)
	"88XXau":    {true, true}, // sysfs driver name varies — both forms observed
	"rtl88XXau": {true, true},
	"rtl8812au": {true, true},
	"rtl8814au": {true, true},
	"88x2bu":    {true, true}, // RTL8812BU
	"rtl88x2bu": {true, true},
	// Older
	"rtl8187": {true, true}, // RTL8187L (AWUS036H)
	"zd1211rw": {true, true},
	"p54usb":   {true, true},
	// Realtek rtw88 stack
	"rtw88_8821au": {true, true},  // RTL8821AU (AWUS036ACS, AWUS036AC)
	"rtw88_8821ce": {true, true},  // RTL8821CE
	"rtw88_8812au": {true, true},  // RTL8812AU via rtw88
	"rtw88_8822bu": {true, false}, // RTL8822BU — monitor ok, inject unreliable
	"rtw88_8822be": {true, false},
	"rtw88_8822ce": {true, false},
	"rtw88_8723de": {true, false},
	// Monitor ok, injection unreliable/no
	"iwlwifi":    {true, false}, // Intel
	"rtl8xxxu":   {true, false},
	"rtw_8822bu": {true, false}, // older alias — keep for compatibility
	// Onboard CZ / Pi — FullMAC, neither without a nexmon patch
	"brcmfmac": {false, false},
}

// adapterDriver reads the bound kernel driver name from sysfs (cheap, no
// subprocess): /sys/class/net/<name>/device/driver is a symlink to it.
func adapterDriver(name string) string {
	p, err := os.Readlink("/sys/class/net/" + name + "/device/driver")
	if err != nil {
		return ""
	}
	return filepath.Base(p)
}

// MonCapIface reports whether the named network interface's driver is in the
// known-good monitor-mode list. Fork-free: one sysfs Readlink. Returns false
// for non-wireless or unknown drivers.
func MonCapIface(name string) bool {
	drv := adapterDriver(name)
	if drv == "" {
		return false
	}
	c, ok := driverCaps[drv]
	return ok && c.mon
}

// Snapshot collects a fresh reading. Cheap enough to call per-request.
func Snapshot() Info {
	var i Info
	i.Hostname, _ = os.Hostname()
	i.Uptime = readUptime()
	i.Load1 = readLoad1()
	i.MemTotalMB, i.MemAvailMB = readMem()
	i.TempC = readTempC()
	i.Battery = readBattery()
	i.GPS = gps.Poll(1200 * time.Millisecond) // > gpsd's ~1 Hz cadence to reliably catch a TPV
	i.Adapters = readAdapters()
	return i
}

func readUptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func readLoad1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func readMem() (total, avail int) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "MemTotal:":
			total = kb / 1024
		case "MemAvailable:":
			avail = kb / 1024
		}
	}
	return
}

func readTempC() float64 {
	b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	milli, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return float64(milli) / 1000.0
}

// readBattery prefers a sysfs power_supply node (stock OS, where a bq27xxx
// driver may be bound), then falls back to a direct-i2c bq27220 read (our Kali
// graft strips the kernel driver, so the chip is reachable only over
// /dev/i2c-1). The voltage→SOC curve matches APPLaunch's so the phone, the LCD,
// and the launcher all agree on the number.
func readBattery() Battery {
	dirs, _ := os.ReadDir("/sys/class/power_supply")
	for _, d := range dirs {
		base := "/sys/class/power_supply/" + d.Name()
		mv, ok1 := readIntFile(base + "/voltage_now")
		if !ok1 {
			continue
		}
		b := Battery{Source: "sysfs", VoltageMV: mv / 1000, Valid: true}
		b.Percent = socFromMV(b.VoltageMV)
		if st, ok := readStringFile(base + "/status"); ok {
			b.Charging = strings.TrimSpace(st) == "Charging"
		}
		return b
	}
	return readBatteryI2C()
}

// readBatteryI2C talks to the bq27220 fuel gauge directly on /dev/i2c-1 at
// address 0x55 — the same registers APPLaunch's HAL uses. Voltage (0x08) is in
// mV; average current (0x14) is signed mA, used as a charge/discharge hint.
func readBatteryI2C() Battery {
	const (
		i2cSlave = 0x0703 // I2C_SLAVE ioctl
		addr     = 0x55   // bq27220
		regVolt  = 0x08
		regAvgI  = 0x14
	)
	fd, err := syscall.Open("/dev/i2c-1", syscall.O_RDWR, 0)
	if err != nil {
		return Battery{Source: "unknown"}
	}
	defer syscall.Close(fd)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cSlave, addr); e != 0 {
		return Battery{Source: "unknown"}
	}

	mv, ok := i2cReadWord(fd, regVolt)
	if !ok || mv == 0 || mv == 0xFFFF {
		return Battery{Source: "unknown"}
	}
	b := Battery{Source: "i2c-bq27220", VoltageMV: mv, Valid: true}
	b.Percent = socFromMV(mv)
	if ai, ok := i2cReadWord(fd, regAvgI); ok {
		cur := ai
		if cur > 32767 {
			cur -= 65536
		}
		b.Charging = cur > 0
	}
	return b
}

// i2cReadWord does a register-addressed 16-bit little-endian read: write the
// register byte, read two bytes back. Works for the bq27220's command regs.
func i2cReadWord(fd, reg int) (int, bool) {
	if _, err := syscall.Write(fd, []byte{byte(reg)}); err != nil {
		return 0, false
	}
	buf := make([]byte, 2)
	if n, err := syscall.Read(fd, buf); err != nil || n != 2 {
		return 0, false
	}
	return int(buf[0]) | int(buf[1])<<8, true
}

// socFromMV is the same Li-ion open-circuit-voltage curve used in APPLaunch's
// HAL, since the bq27220's on-chip SOC register is uncalibrated.
func socFromMV(mv int) int {
	type pt struct{ mv, pct int }
	curve := []pt{
		{4200, 100}, {4100, 90}, {4000, 80}, {3900, 65}, {3800, 50},
		{3700, 35}, {3600, 20}, {3500, 10}, {3400, 5}, {3300, 2}, {3200, 0},
	}
	if mv >= curve[0].mv {
		return 100
	}
	last := curve[len(curve)-1]
	if mv <= last.mv {
		return 0
	}
	for i := 1; i < len(curve); i++ {
		if mv >= curve[i].mv {
			dv := curve[i-1].mv - curve[i].mv
			dp := curve[i-1].pct - curve[i].pct
			return curve[i].pct + (mv-curve[i].mv)*dp/dv
		}
	}
	return 0
}

func readAdapters() []Adapter {
	var out []Adapter
	nics, _ := os.ReadDir("/sys/class/net")
	for _, n := range nics {
		name := n.Name()
		if name == "lo" {
			continue
		}
		a := Adapter{Name: name, Type: "other"}
		if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
			a.Type = "wifi"
		} else if _, err := os.Stat("/sys/class/net/" + name + "/device"); err == nil {
			a.Type = "ethernet"
		}
		// ARPHRD link type: 803 == IEEE80211_RADIOTAP == monitor mode active.
		// This is a real signal (vs. the old "has /wireless" guess), so it only
		// lights up for interfaces actually capturing, like wlanNmon.
		if t, ok := readIntFile("/sys/class/net/" + name + "/type"); ok && t == 803 {
			a.Type = "wifi"
			a.Monitor = true
		}
		// Driver-inferred monitor / injection capability.
		if d := adapterDriver(name); d != "" {
			a.Driver = d
			if c, ok := driverCaps[d]; ok {
				a.MonCap = c.mon
				a.Inject = c.inject
			}
		}
		if st, ok := readStringFile("/sys/class/net/" + name + "/operstate"); ok {
			a.Up = strings.TrimSpace(st) == "up"
		}
		out = append(out, a)
	}
	return out
}

// AdapterDetail is the rich per-interface readout for the click-through modal.
type AdapterDetail struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Driver    string   `json:"driver"`
	MAC       string   `json:"mac"`
	MTU       int      `json:"mtu"`
	State     string   `json:"state"`
	SpeedMbps int      `json:"speed_mbps"` // -1 if n/a (e.g. wifi)
	Monitor   bool     `json:"monitor"`
	MonCap    bool     `json:"mon_cap"`
	Inject    bool     `json:"inject"`
	IPs       []string `json:"ips"`
	RxBytes   int64    `json:"rx_bytes"`
	TxBytes   int64    `json:"tx_bytes"`
	RxPackets int64    `json:"rx_packets"`
	TxPackets int64    `json:"tx_packets"`
	RxErrors  int64    `json:"rx_errors"`
	TxErrors  int64    `json:"tx_errors"`
	RxDropped int64    `json:"rx_dropped"`
	TxDropped int64    `json:"tx_dropped"`
}

// Detail gathers the full readout for one interface (modal). Returns ok=false
// if the interface doesn't exist.
func Detail(name string) (AdapterDetail, bool) {
	base := "/sys/class/net/" + name
	if _, err := os.Stat(base); err != nil {
		return AdapterDetail{}, false
	}
	d := AdapterDetail{Name: name, Type: "other", SpeedMbps: -1}

	if _, err := os.Stat(base + "/wireless"); err == nil {
		d.Type = "wifi"
	} else if _, err := os.Stat(base + "/device"); err == nil {
		d.Type = "ethernet"
	}
	if t, ok := readIntFile(base + "/type"); ok && t == 803 {
		d.Type, d.Monitor = "wifi", true
	}
	if drv := adapterDriver(name); drv != "" {
		d.Driver = drv
		if c, ok := driverCaps[drv]; ok {
			d.MonCap, d.Inject = c.mon, c.inject
		}
	}
	if s, ok := readStringFile(base + "/address"); ok {
		d.MAC = strings.TrimSpace(s)
	}
	if v, ok := readIntFile(base + "/mtu"); ok {
		d.MTU = v
	}
	if s, ok := readStringFile(base + "/operstate"); ok {
		d.State = strings.TrimSpace(s)
	}
	if v, ok := readIntFile(base + "/speed"); ok && v > 0 {
		d.SpeedMbps = v
	}

	rd := func(f string) int64 { v, _ := readIntFile(base + "/statistics/" + f); return int64(v) }
	d.RxBytes, d.TxBytes = rd("rx_bytes"), rd("tx_bytes")
	d.RxPackets, d.TxPackets = rd("rx_packets"), rd("tx_packets")
	d.RxErrors, d.TxErrors = rd("rx_errors"), rd("tx_errors")
	d.RxDropped, d.TxDropped = rd("rx_dropped"), rd("tx_dropped")

	if iface, err := net.InterfaceByName(name); err == nil {
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				d.IPs = append(d.IPs, a.String())
			}
		}
	}
	return d, true
}

func readIntFile(p string) (int, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return v, err == nil
}

func readStringFile(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}
