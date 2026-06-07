// Package web embeds the front-end PWA into the binary so czconsole ships as a
// single file. The real dashboard lands after the wardrive UX is signed off;
// for now static/ holds a placeholder that exercises /api/sysinfo.
package web

import "embed"

//go:embed static
var Files embed.FS
