package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/opdir"
	"github.com/n0xa/czconsole/internal/unit"
)

// jobGroup is the shared group between the worker (writes the job file) and the
// operator (run-tool reads it). The job must be group-readable or run-tool gets
// an empty value map and every {{input}} resolves to "".
const jobGroup = "czconsole"

// Runner is the FRONTEND side of a spec (LCD or web): it writes the worker's
// input values to the job file and starts the matching cap-class unit, checks
// run-state, stops, and finds the latest output. It holds no privilege — the
// privileged action is the polkit-authorized systemctl start, and the command +
// caps live in the trusted spec read by run-tool.
type Runner struct{ spec Spec }

func NewRunner(s Spec) *Runner { return &Runner{spec: s} }

func (r *Runner) Spec() Spec { return r.spec }

// Unit is the systemd unit instance for this tool's class.
func (r *Runner) Unit() string {
	cls := ""
	if r.spec.Class == ClassNetRaw {
		cls = "-netraw"
	}
	return "czconsole-tool" + cls + "@" + r.spec.ID + ".service"
}

func (r *Runner) jobPath() string { return filepath.Join("/run/czconsole", r.spec.ID+".json") }

// Running reports whether this tool's unit has live processes (cgroup read).
func (r *Runner) Running() bool {
	active, _ := unit.CgroupActive(r.Unit())
	return active
}

// Start writes the input values and starts the unit. Values are stripped of
// control characters (defensive — run-tool passes them as literal argv, so they
// can't inject, but a NUL would truncate an arg).
func (r *Runner) Start(vals map[string]string) error {
	if r.Running() {
		return fmt.Errorf("already running")
	}
	clean := make(map[string]string, len(vals))
	for k, v := range vals {
		clean[k] = stripCtl(v)
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	// 0640 + group czconsole: run-tool runs as the operator (User=kali), not
	// _czconsole, so the job must be readable via the shared group or all input
	// values are silently dropped (every {{input}} -> "").
	if err := os.WriteFile(r.jobPath(), b, 0o640); err != nil {
		return fmt.Errorf("job file: %w", err)
	}
	if g, err := user.LookupGroup(jobGroup); err == nil {
		if gid, e := strconv.Atoi(g.Gid); e == nil {
			_ = os.Chown(r.jobPath(), -1, gid) // _czconsole is in czconsole, so this is permitted
		}
	}
	// Force the mode: the worker runs with a restrictive umask (UMask=0077), which
	// would strip group-read from WriteFile's 0640 down to 0600 — chmod ignores umask.
	_ = os.Chmod(r.jobPath(), 0o640)
	if out, err := exec.Command("systemctl", "start", "--no-block", r.Unit()).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl start: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop cancels a running tool.
func (r *Runner) Stop() error { return exec.Command("systemctl", "stop", r.Unit()).Run() }

// Values reads back the last job's input values (for the running/results header).
func (r *Runner) Values() map[string]string {
	b, err := os.ReadFile(r.jobPath())
	if err != nil {
		return nil
	}
	var v map[string]string
	_ = json.Unmarshal(b, &v)
	return v
}

// Subject renders the spec's running.subject template against the last job's
// values — what's being scanned, shown in the running and results headers.
func (r *Runner) Subject() string {
	return expandString(r.spec.Running.Subject, r.Values())
}

// Latest returns the newest output file in ~/<id> matching the spec's
// results.file suffix, and its mtime ("" if none yet).
func (r *Runner) Latest() (string, time.Time) {
	dir := opdir.Tool(r.spec.ID)
	entries, _ := os.ReadDir(dir)
	var newest string
	var newestT time.Time
	for _, e := range entries {
		if e.IsDir() || (r.spec.Results.File != "" && !strings.HasSuffix(e.Name(), r.spec.Results.File)) {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newestT) {
			newest, newestT = filepath.Join(dir, e.Name()), fi.ModTime()
		}
	}
	return newest, newestT
}

func stripCtl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
}
