// Package gobuster is the privilege-free orchestration core for gobuster dir
// scans, shared by both frontends. It builds the command from three inputs
// (target URL, wordlist, extra options), runs it via the per-tool systemd unit
// (operator-run; no special caps — plain HTTP), detects whether a scan is live
// (fork-free cgroup read), and parses the captured output into a compact result.
//
// The wrapper captures the whole run (banner + findings + errors) to one
// ~/gobuster/gobuster-<ts>.txt, so the result is self-describing: the banner's
// "[+] Url:" / "[+] Wordlist:" lines give the header, the "… (Status: NNN)"
// lines the findings.
package gobuster

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/opdir"
	"github.com/n0xa/czconsole/internal/unit"
)

const (
	unitName = "czconsole-gobuster.service"
	envPath  = "/run/czconsole/gobuster.env"

	// DefaultWordlist prefills the wordlist field (a standard Kali list); the
	// operator can edit it. DefaultOptions prefills the options field.
	DefaultWordlist = "/usr/share/wordlists/dirb/common.txt"
	DefaultOptions  = "-t 50"
)

type Core struct{}

func New() *Core { return &Core{} }

// OutputDir is the operator's ~/gobuster.
func OutputDir() string { return opdir.Tool("gobuster") }

// Running reports whether a scan is live (cgroup read; no fork).
func (c *Core) Running() bool {
	active, _ := unit.CgroupActive(unitName)
	return active
}

// RunningTarget returns the in-flight scan's target URL, read from the env file
// it was launched with — so the running view shows what's being scanned even
// after a Background + re-entry.
func (c *Core) RunningTarget() string { return envVar(envPath, "GOBUSTER_URL") }

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

// Finding is one discovered path and its HTTP status.
type Finding struct {
	Path   string `json:"path"`
	Status int    `json:"status"`
}

// Result is a parsed gobuster run.
type Result struct {
	File     string    `json:"file"`
	Target   string    `json:"target"`
	Wordlist string    `json:"wordlist"`
	When     time.Time `json:"when"`
	Findings []Finding `json:"findings"`
}

// LatestResult parses the newest output in OutputDir, or (nil, nil) if none yet.
func (c *Core) LatestResult() (*Result, error) {
	f := newestFile(OutputDir(), ".txt")
	if f == "" {
		return nil, nil
	}
	return parseFile(f)
}

func newestFile(dir, suffix string) string {
	entries, _ := os.ReadDir(dir)
	var newest string
	var newestT time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		fi, err := e.Info()
		if err == nil && fi.ModTime().After(newestT) {
			newest, newestT = dir+"/"+e.Name(), fi.ModTime()
		}
	}
	return newest
}

func parseFile(path string) (*Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	res := &Result{File: path}
	if fi, err := os.Stat(path); err == nil {
		res.When = fi.ModTime()
	}
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "[+] Url:"); ok {
			res.Target = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(t, "[+] Wordlist:"); ok {
			res.Wordlist = strings.TrimSpace(v)
		} else if i := strings.Index(t, "(Status:"); i > 0 {
			p := strings.TrimSpace(t[:i])
			st := leadingInt(strings.TrimSpace(t[i+len("(Status:"):]))
			res.Findings = append(res.Findings, Finding{Path: p, Status: st})
		}
	}
	return res, nil
}

// leadingInt parses the integer at the start of s (e.g. "301) [Size: …" → 301).
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// Start launches a dir scan. URL and wordlist are single values (the wrapper
// quotes them — injection-safe); opts is word-split into argv like nmap (control
// operators inert). All three are stripped of newlines/control chars so they
// can't corrupt the single-line env file.
func (c *Core) Start(url, wordlist, opts string) error {
	if c.Running() {
		return fmt.Errorf("a scan is already running")
	}
	url, wordlist, opts = clean(url), clean(wordlist), clean(opts)
	if url == "" {
		return fmt.Errorf("target URL required")
	}
	if wordlist == "" {
		return fmt.Errorf("wordlist required")
	}
	env := fmt.Sprintf("GOBUSTER_URL=%s\nGOBUSTER_WORDLIST=%s\nGOBUSTER_OPTS=%s\n", url, wordlist, opts)
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

func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r < 0x20 {
			return ' '
		}
		return r
	}, s))
}
