// Command czconsole-runtool is the trusted tool runner. It is run as the operator
// by the czconsole-tool[-netraw]@<id> systemd unit (ExecStart=… %i). It:
//
//  1. reads the TRUSTED spec for <id> from /etc/czconsole/tools.d (the only place
//     the command definition comes from — never the worker),
//  2. refuses if its effective caps exceed the spec's class (defense-in-depth
//     against being started under the wrong unit),
//  3. reads the worker-supplied input VALUES from /run/czconsole/<id>.json,
//  4. substitutes them into the spec's argv and runs it — as an argv array via
//     exec, NEVER a shell, so values cannot inject — capturing the combined
//     stdout+stderr to ~/<id>/<id>-<ts>.output (tools that write their own files
//     use {{outfile}}),
//  5. runs an optional post step (e.g. rtl_power's heatmap), gated on an input.
//
// The worker only ever supplies the tool id + input values; the binary and arg
// structure are trusted config. That's the privsep boundary (the operator has
// sudo→root, so the worker must not choose commands). See Deck #275.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/tool"
)

func main() {
	if len(os.Args) < 2 {
		die("usage: czconsole-runtool <tool-id>")
	}
	id := os.Args[1]

	spec, err := tool.LoadByID(id)
	if err != nil {
		die("spec: %v", err)
	}

	// Defense-in-depth: never run with more privilege than the spec's class.
	if eff, err := effectiveCaps(); err == nil {
		if eff&^tool.AllowedCapMask(spec.Class) != 0 {
			die("refusing: effective caps %#x exceed class %q for %q", eff, spec.Class, id)
		}
	}

	vals := readJob(id)

	home, err := os.UserHomeDir()
	if err != nil {
		die("home: %v", err)
	}
	dir := filepath.Join(home, id)
	if err := os.MkdirAll(dir, 0o2775); err != nil {
		die("mkdir %s: %v", dir, err)
	}
	outfile := filepath.Join(dir, id+"-"+time.Now().Format("2006-01-02-15-04-05"))

	argv := tool.Substitute(spec.Command.Argv, vals, outfile)
	if len(argv) == 0 {
		die("empty command for %q", id)
	}
	code := run(argv, outfile+".output", false)

	if spec.Post != nil && (spec.Post.When == "" || vals[spec.Post.When] == "1") {
		if pargv := tool.Substitute(spec.Post.Argv, vals, outfile); len(pargv) > 0 {
			run(pargv, outfile+".output", true)
		}
	}
	os.Exit(code)
}

// run executes argv, sending combined stdout+stderr to outpath. Returns the exit code.
func run(argv []string, outpath string, appnd bool) int {
	flag := os.O_CREATE | os.O_WRONLY
	if appnd {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(outpath, flag, 0o644)
	if err != nil {
		die("open %s: %v", outpath, err)
	}
	defer f.Close()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(f, "\nruntool: %v\n", err)
		return 1
	}
	return 0
}

// readJob reads the worker-supplied input values for id (or an empty map if the
// job file is absent — a tool with no inputs).
func readJob(id string) map[string]string {
	b, err := os.ReadFile(filepath.Join("/run/czconsole", id+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{} // a tool with no inputs has no job file — fine
		}
		// e.g. a permission error: do NOT silently run with empty inputs (every
		// {{input}} would become ""); fail loudly instead.
		die("read job %s: %v", id, err)
	}
	var vals map[string]string
	if err := json.Unmarshal(b, &vals); err != nil || vals == nil {
		return map[string]string{}
	}
	return vals
}

// effectiveCaps reads CapEff from /proc/self/status.
func effectiveCaps() (uint64, error) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "CapEff:"); ok {
			return strconv.ParseUint(strings.TrimSpace(v), 16, 64)
		}
	}
	return 0, fmt.Errorf("CapEff not found")
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "czconsole-runtool: "+format+"\n", a...)
	os.Exit(1)
}
