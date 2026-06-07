package modules

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
)

// filesProxy is the Files module as seen by the *web worker* (running as the
// unprivileged _czconsole user, which cannot write the operator's home). It
// reverse-proxies /m/files/… over a unix socket to the files-agent process,
// which runs as the operator (kali/pi) and does the actual file I/O with
// correct ownership. This is the privsep split: the network-facing worker never
// touches the home directory; the small agent does, as the user.
type filesProxy struct {
	sock string
}

func NewFilesProxy(sock string) *filesProxy { return &filesProxy{sock: sock} }

func (p *filesProxy) Manifest() Manifest { return filesManifest() }

func (p *filesProxy) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	rp := &httputil.ReverseProxy{
		// The agent serves the same /m/files/… paths, so no path rewrite —
		// only point the request at the (irrelevant) unix "host".
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "files-agent"
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", p.sock)
			},
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "files agent unavailable: "+err.Error(), http.StatusBadGateway)
		},
	}
	// Auth gates the proxy; the agent's socket is reachable only by the worker
	// (socket group perms), so the agent itself need not re-authenticate.
	mux.Handle(prefix, auth(rp.ServeHTTP))
}
