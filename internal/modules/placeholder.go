package modules

import (
	"html"
	"net/http"
)

// placeholderModule renders a module's tile on the dashboard but its detail
// page is a "coming soon" stub. Used for modules whose real implementation
// hasn't landed yet, so available tiles open to something coherent instead of
// a 404.
type placeholderModule struct{ man Manifest }

func newPlaceholder(m Manifest) *placeholderModule { return &placeholderModule{man: m} }

func (p *placeholderModule) Manifest() Manifest { return p.man }

func (p *placeholderModule) Mount(mux *http.ServeMux, prefix string, _ Middleware) {
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8">` +
			`<meta name="viewport" content="width=device-width,initial-scale=1">` +
			`<link rel="stylesheet" href="/style.css"><title>` + html.EscapeString(p.man.Name) + `</title></head>` +
			`<body><header class="appbar"><a class="back" href="/" style="color:var(--dim);text-decoration:none">‹</a>` +
			`<h1>` + html.EscapeString(p.man.Name) + `</h1></header>` +
			`<main class="wrap"><section class="card"><h2>Coming soon</h2>` +
			`<p class="dim">` + html.EscapeString(p.man.Description) + `</p>` +
			`<p class="dim">This module's backend isn't wired up yet.</p>` +
			`</section></main></body></html>`))
	})
}
