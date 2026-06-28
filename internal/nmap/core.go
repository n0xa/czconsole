// Package nmap is the privilege-free orchestration core for nmap scans, shared by
// both frontends (web module + native LCD): build the scan command from the
// operator's CLI options, start it via the per-tool systemd unit (operator-run,
// CAP_NET_RAW + --privileged), detect whether a scan is running (fork-free cgroup
// read), and parse the newest -oA XML into a compact result.
//
// It holds NO privilege itself: the scan runs as the operator via systemd, and
// the only state it touches is the operator's ~/nmap output dir (the scan writes
// it; the frontends read it). The privilege boundary stays in the unit's scoped
// caps, exactly like wardrive's kismet unit.
package nmap

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/opdir"
	"github.com/n0xa/czconsole/internal/unit"
)

const (
	unitName = "czconsole-nmap.service"
	envPath  = "/run/czconsole/nmap.env"
)

// Core is an nmap controller. Stateless beyond the filesystem, so the zero value
// (and New) are both fine; safe for concurrent reads.
type Core struct{}

func New() *Core { return &Core{} }

// OutputDir is the operator's ~/nmap, where -oA writes nmap-<ts>.{xml,nmap,gnmap}
// and the optional .err. Created (setgid czconsole, group-readable) by the
// package postinstall so the deprivileged frontends can read the results.
func OutputDir() string { return opdir.Tool("nmap") }

// Running reports whether a scan is live (cgroup read; no fork).
func (c *Core) Running() bool {
	active, _ := unit.CgroupActive(unitName)
	return active
}

// RunningOpts returns the in-flight scan's options, read from the env file it was
// launched with — so the running view shows what's being scanned even after a
// Background + re-entry (a fresh screen with no memory of the inputs).
func (c *Core) RunningOpts() string { return envVar(envPath, "NMAP_OPTS") }

// envVar reads KEY=value from a single-line env file (the one Start wrote).
func envVar(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ── results model (compact, for both frontends) ──────────────────────────────

// Port is one reported port.
type Port struct {
	Num     int    `json:"num"`
	Proto   string `json:"proto"`
	State   string `json:"state"` // open | filtered | open|filtered | closed …
	Service string `json:"service"`
}

// Host is one scanned host: its address, up/down, the explicitly-reported ports,
// and the bulk "closed" count. A ping sweep (-sn) yields many hosts with no
// ports; a port scan of one target yields one host with ports.
type Host struct {
	Addr   string `json:"addr"`
	Up     bool   `json:"up"`
	Ports  []Port `json:"ports"`  // open/filtered (closed collapsed into Closed)
	Closed int    `json:"closed"` // <extraports state=closed>
}

// Result is a parsed scan: every host nmap reported. Mirrors nmap's own output —
// closed ports collapse into Host.Closed; down hosts are typically absent.
type Result struct {
	File  string    `json:"file"`  // the .xml path it came from
	Args  string    `json:"args"`  // nmap command line (from <nmaprun>)
	When  time.Time `json:"when"`  // scan start
	Hosts []Host    `json:"hosts"` // one per <host> element
}

// LatestResult parses the newest scan XML in OutputDir, or (nil, nil) if there
// are no scans yet (a config-first state, not an error).
func (c *Core) LatestResult() (*Result, error) {
	f := newestFile(OutputDir(), "*.xml")
	if f == "" {
		return nil, nil
	}
	return parseFile(f)
}

func newestFile(dir, glob string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, glob))
	var newest string
	var newestT time.Time
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newestT) {
			newest, newestT = p, fi.ModTime()
		}
	}
	return newest
}

// ── nmap -oX schema (only the fields we render) ──────────────────────────────

type xmlRun struct {
	Args  string    `xml:"args,attr"`
	Start int64     `xml:"start,attr"`
	Hosts []xmlHost `xml:"host"`
}
type xmlHost struct {
	Status struct {
		State string `xml:"state,attr"`
	} `xml:"status"`
	Address []struct {
		Addr string `xml:"addr,attr"`
		Type string `xml:"addrtype,attr"`
	} `xml:"address"`
	Ports struct {
		Extra []struct {
			State string `xml:"state,attr"`
			Count int    `xml:"count,attr"`
		} `xml:"extraports"`
		Port []xmlPort `xml:"port"`
	} `xml:"ports"`
}
type xmlPort struct {
	Protocol string `xml:"protocol,attr"`
	PortID   int    `xml:"portid,attr"`
	State    struct {
		State string `xml:"state,attr"`
	} `xml:"state"`
	Service struct {
		Name string `xml:"name,attr"`
	} `xml:"service"`
}

func parseFile(path string) (*Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run xmlRun
	if err := xml.Unmarshal(b, &run); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	res := &Result{File: path, Args: run.Args}
	if run.Start > 0 {
		res.When = time.Unix(run.Start, 0)
	}
	for _, xh := range run.Hosts {
		h := Host{Up: xh.Status.State == "up"}
		for _, a := range xh.Address {
			if a.Type == "ipv4" || a.Type == "ipv6" || h.Addr == "" {
				h.Addr = a.Addr
			}
		}
		for _, e := range xh.Ports.Extra {
			if e.State == "closed" {
				h.Closed += e.Count
			}
		}
		for _, p := range xh.Ports.Port {
			svc := p.Service.Name
			if svc == "" {
				svc = "-"
			}
			h.Ports = append(h.Ports, Port{
				Num: p.PortID, Proto: p.Protocol, State: p.State.State, Service: svc,
			})
		}
		res.Hosts = append(res.Hosts, h)
	}
	return res, nil
}

// ── start a scan ─────────────────────────────────────────────────────────────

// Start launches a scan with the operator's CLI options. The options become
// nmap's argv via the wrapper (word-split, NOT a shell line — control operators
// are inert), and the wrapper adds -oA so the results land in ~/nmap. logErrors
// redirects stderr to a sibling .err file.
//
// Refuses if a scan is already running, and sanitizes the options so they can't
// break out of the single-line env file.
func (c *Core) Start(opts string, logErrors bool) error {
	if c.Running() {
		return fmt.Errorf("a scan is already running")
	}
	opts = sanitizeOpts(opts)
	if opts == "" {
		return fmt.Errorf("no scan options given")
	}
	errs := "0"
	if logErrors {
		errs = "1"
	}
	env := fmt.Sprintf("NMAP_OPTS=%s\nNMAP_LOG_ERRORS=%s\n", opts, errs)
	if err := os.WriteFile(envPath, []byte(env), 0o600); err != nil {
		return fmt.Errorf("env file: %w", err)
	}
	if out, err := exec.Command("systemctl", "start", "--no-block", unitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl start: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop cancels a running scan.
func (c *Core) Stop() error {
	return exec.Command("systemctl", "stop", unitName).Run()
}

// sanitizeOpts strips anything that would corrupt the single-line env file
// (newlines, control chars) and trims surrounding space. It deliberately does
// NOT try to whitelist nmap flags — on the physical console the operator already
// has full nmap; the web frontend is the surface where a flag policy may later
// be wanted (--script / -oN to arbitrary paths are nmap-native powers).
func sanitizeOpts(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r < 0x20 {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
