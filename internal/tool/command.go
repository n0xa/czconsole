package tool

import (
	"regexp"
	"strings"
)

var (
	// {{name}} — single-value token, replaced inline (one argv element).
	tokenRe = regexp.MustCompile(`{{([a-zA-Z0-9_]+)}}`)
	// an element that is EXACTLY {{name...}} — word-split that value into args.
	splitRe = regexp.MustCompile(`^{{([a-zA-Z0-9_]+)\.\.\.}}$`)
)

// expandString replaces {{name}} tokens in a plain string with input values —
// for the running/results subject header. Display text, so no word-splitting.
func expandString(tmpl string, vals map[string]string) string {
	return tokenRe.ReplaceAllStringFunc(tmpl, func(tok string) string {
		return vals[tok[2:len(tok)-2]]
	})
}

// Substitute expands a spec's argv template against the input values and the
// per-run outfile base path:
//
//   - an element exactly "{{id...}}" word-splits that value into 0+ args
//   - "{{outfile}}" and "{{id}}" tokens are replaced inline (one arg each)
//   - literals pass through
//
// The result goes STRAIGHT to exec as an argv array — never a shell — so a value
// can never inject: control operators (;, |, $(…)) become literal arguments to
// the tool, not new commands. This is strictly safer than the old bash wrappers.
func Substitute(argv []string, vals map[string]string, outfile string) []string {
	out := make([]string, 0, len(argv))
	for _, el := range argv {
		if m := splitRe.FindStringSubmatch(el); m != nil {
			out = append(out, strings.Fields(vals[m[1]])...)
			continue
		}
		out = append(out, tokenRe.ReplaceAllStringFunc(el, func(tok string) string {
			name := tok[2 : len(tok)-2]
			if name == "outfile" {
				return outfile
			}
			return vals[name]
		}))
	}
	return out
}
