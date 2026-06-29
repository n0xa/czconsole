package lcd

import (
	"image/color"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/tool"
)

// colorRule colours an output line whose text matches re. Compiled once per spec.
type colorRule struct {
	re  *regexp.Regexp
	col color.Color
}

func compileColors(spec tool.Spec) []colorRule {
	var out []colorRule
	for _, cr := range spec.Results.Colorize {
		if re, err := regexp.Compile(cr.Match); err == nil {
			out = append(out, colorRule{re, namedColor(cr.Color)})
		}
	}
	return out
}

func namedColor(name string) color.Color {
	switch name {
	case "accent":
		return colAccent
	case "dim":
		return colDim
	case "title":
		return colTitle
	default:
		return colText
	}
}

func colorFor(line string, rules []colorRule) color.Color {
	for _, cr := range rules {
		if cr.re.MatchString(line) {
			return cr.col
		}
	}
	return colText
}

// imageSibling returns the kind=image companion file for a run's primary output
// (e.g. <run>.png next to <run>.csv), or "" if there's no image / kind != image.
func imageSibling(spec tool.Spec, primaryPath string) string {
	if spec.Results.Kind != "image" || spec.Results.Image == "" {
		return ""
	}
	ip := strings.TrimSuffix(primaryPath, spec.Results.File) + spec.Results.Image
	if _, err := os.Stat(ip); err == nil {
		return ip
	}
	return ""
}

// resultLines renders one run's output per the spec — the shared body used by
// both the live results page and the history viewer. subject is "" for historical
// runs (the job file only holds the latest run's inputs).
func resultLines(spec tool.Spec, primaryPath string, when time.Time, subject, imagePath string, colors []colorRule) []Line {
	var lines []Line
	if subject != "" {
		lines = append(lines, Line{subject, colText})
	}
	if !when.IsZero() {
		lines = append(lines, Line{when.Format("2006-01-02 15:04:05"), colDim})
	}

	switch spec.Results.Kind {
	case "image":
		if imagePath != "" {
			return append(lines, Line{"heatmap ready — press ent to view", colAccent})
		}
		return append(lines, Line{"no heatmap for this run", colDim},
			Line{"saved to:", colDim}, Line{primaryPath, colDim})
	case "path":
		return append(lines, Line{"saved to:", colDim}, Line{primaryPath, colAccent})
	}

	// text
	b, err := os.ReadFile(primaryPath)
	if err != nil {
		return append(lines, Line{"(output unavailable)", colDim})
	}
	sp := spec.Results.StripPrefix
	for _, raw := range strings.Split(string(b), "\n") {
		ln := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if sp != "" && strings.HasPrefix(strings.TrimSpace(ln), sp) {
			continue
		}
		lines = append(lines, Line{ln, colorFor(ln, colors)})
	}
	return lines
}
