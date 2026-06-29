// Package tool is the runtime, JSON-driven tool framework: a Spec describes a
// menu tool end to end — menu placement, config-page inputs, the command + argv
// template, the capability class it runs under, and how to render its output —
// so adding a tool is a JSON drop-in, not Go code.
//
// TRUST BOUNDARY (load-bearing): specs are TRUSTED config. The operator has
// sudo→root, so the deprivileged worker must never choose the *command* that
// runs as the operator — only the input VALUES. Specs therefore live root-owned
// in SpecDir; the worker/LCD reads them to render the UI and writes only values
// (to the job file), while the trusted run-tool reads the same spec to build and
// exec the command, dropping to the spec's caps. See Deck #275 + docs/native-lcd.md.
package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// SpecDir is the root-owned directory of trusted tool specs (*.json).
const SpecDir = "/etc/czconsole/tools.d"

// Capability classes — the fixed set of systemd unit templates a tool can run
// under. The unit grants these as a ceiling; run-tool drops to exactly the
// spec's class. Kept tiny on purpose (least privilege).
const (
	ClassPlain  = "plain"  // no caps (gobuster, rtl_433, rtl_power)
	ClassNetRaw = "netraw" // CAP_NET_RAW (nmap, tcpdump)
)

// Spec describes one tool end to end (see package doc for the trust model).
type Spec struct {
	ID     string `json:"id"`     // stable id: the systemd instance + ~/<id> output dir
	Name   string `json:"name"`   // menu label
	Group  string `json:"group"`  // menu grouping, e.g. "Net Recon" / "Wireless"
	Binary string `json:"binary"` // availability probe (exec.LookPath)
	Class  string `json:"class"`  // ClassPlain | ClassNetRaw

	Inputs  []Input `json:"inputs"`
	Command Command `json:"command"`
	Results Results `json:"results"`
	Running Running `json:"running"`
	Post    *Post   `json:"post,omitempty"`
}

// Input is one config-page field.
type Input struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Type        string `json:"type"`        // "text" | "checkbox"
	Default     string `json:"default"`     // text default; "1"/"0" for checkbox
	Placeholder string `json:"placeholder"` // hint shown when a text field is empty
	Required    bool   `json:"required"`    // submit blocked until this text field is non-empty
}

// Command is the argv template. Each element is a literal or carries tokens:
//
//	{{outfile}}       → the per-run ~/<id>/<id>-<ts> base path (extension via Results.File)
//	{{<input-id>}}    → that input's value as ONE argv element
//	{{<input-id>...}} → (element is exactly "{{id...}}") value WORD-SPLIT into argv
//
// run-tool substitutes: literals pass through; single-value tokens are one argv
// each; the "..." form word-splits. Control operators in a value are inert (they
// become literal args, never shell syntax) — today's injection-safety, made
// declarative. stdout+stderr are always captured to {{outfile}}.stdout.
type Command struct {
	Argv []string `json:"argv"`
}

// Results says how the results page renders the run's output.
type Results struct {
	Kind        string      `json:"kind"`         // "text" (show output) | "path" (file location) | "image" (pan viewer)
	File        string      `json:"file"`         // outfile suffix to read / point at: ".nmap", ".stdout", ".pcap", ".csv"
	Image       string      `json:"image"`        // (kind=image) sibling-image suffix to view, e.g. ".png"; falls back to File's path if absent
	StripPrefix string      `json:"strip_prefix"` // drop output lines starting with this (e.g. "#")
	Colorize    []ColorRule `json:"colorize"`     // regex → colour, at-a-glance highlighting (display only, NOT parsing)
}

// ColorRule colours any output line matching Match.
type ColorRule struct {
	Match string `json:"match"` // RE2 regexp
	Color string `json:"color"` // "accent" | "dim" | "text" | "title"
}

// Running configures the live view.
type Running struct {
	Label    string `json:"label"`    // e.g. "SCANNING…", "CAPTURING…"
	Subject  string `json:"subject"`  // template of input ids to show, e.g. "{{opts}}" / "{{url}}"
	Controls string `json:"controls"` // "cancel-background" (one-shot) | "stop" (continuous)
}

// Controls values.
const (
	ControlsCancelBg = "cancel-background"
	ControlsStop     = "stop"
)

// Post is an optional follow-up command run after the main one finishes, gated on
// an input (e.g. rtl_power's heatmap: run rfheatmap when the heatmap box is on).
type Post struct {
	When string   `json:"when"` // input id that must be "1" for Post to run ("" = always)
	Argv []string `json:"argv"` // same templating as Command.Argv
}

// Input returns the input with the given id.
func (s Spec) Input(id string) (Input, bool) {
	for _, in := range s.Inputs {
		if in.ID == id {
			return in, true
		}
	}
	return Input{}, false
}

var idRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// LoadByID reads the single TRUSTED spec for id from SpecDir. id must be a bare
// name (no path separators) so a worker-supplied id can't traverse out of the
// trusted dir; the spec's own id must also match. This is run-tool's only input
// path for the command definition.
func LoadByID(id string) (Spec, error) {
	if !idRe.MatchString(id) {
		return Spec{}, fmt.Errorf("invalid tool id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(SpecDir, id+".json"))
	if err != nil {
		return Spec{}, err
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return Spec{}, err
	}
	if s.ID != id {
		return Spec{}, fmt.Errorf("spec id %q does not match file %q", s.ID, id)
	}
	return s, nil
}

// Load reads every valid *.json spec from dir (default SpecDir), sorted by id.
// Malformed or id-less files are skipped so one bad drop-in can't break the menu.
func Load(dir string) ([]Spec, error) {
	if dir == "" {
		dir = SpecDir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var specs []Spec
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Spec
		if err := json.Unmarshal(b, &s); err != nil || s.ID == "" {
			continue
		}
		specs = append(specs, s)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs, nil
}
