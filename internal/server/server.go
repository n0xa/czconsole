// Package server wires the HTTP surface: the embedded PWA, the JSON API, the
// LCD mirror, and the module host. It is deliberately stdlib-only so the whole
// binary cross-compiles with `GOOS=linux GOARCH=arm64 go build` and no cgo.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/n0xa/czconsole/internal/auth"
	"github.com/n0xa/czconsole/internal/fb"
	"github.com/n0xa/czconsole/internal/gps"
	"github.com/n0xa/czconsole/internal/modules"
	"github.com/n0xa/czconsole/internal/sysinfo"
	"github.com/n0xa/czconsole/web"
)

type Config struct {
	Listen    string
	AuthToken string

	// Optional PAM login layer. When RequireLogin is set, Sessions + Verifier
	// must be non-nil and every route except /login, /logout and /healthz
	// demands a valid session cookie (or the AuthToken, for API clients).
	RequireLogin bool
	Sessions     *auth.Sessions
	Verifier     *auth.Verifier
	Limiter      *auth.RateLimiter
	SecureCookie bool // set the session cookie Secure flag (TLS deployments)

	Modules *modules.Registry
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	// Module routes first (/m/<id>/…). These are more specific than "/" so the
	// dashboard file server never shadows them.
	s.cfg.Modules.Mount(s.mux, s.auth)

	// Embedded PWA (web/ at build time). Gated too, so the dashboard shell
	// itself requires a session when login is enforced.
	content, _ := fs.Sub(web.Files, "static")
	s.mux.Handle("/", s.auth(http.FileServer(http.FS(content)).ServeHTTP))

	// Open endpoints (never gated): liveness + the login flow itself.
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)

	// Core API — present on every device regardless of OS.
	s.mux.HandleFunc("/api/sysinfo", s.auth(s.handleSysinfo))
	s.mux.HandleFunc("/api/modules", s.auth(s.handleModules))
	s.mux.HandleFunc("/api/gps", s.auth(s.handleGPS))
	s.mux.HandleFunc("/api/adapter", s.auth(s.handleAdapter))
	s.mux.HandleFunc("/api/whoami", s.auth(s.handleWhoami))

	// LCD mirror: live JPEG of /dev/fb0. Cheap enough to poll or MJPEG-stream.
	s.mux.HandleFunc("/api/display.jpg", s.auth(s.handleDisplay))
}

// authorized reports whether the request is allowed through. Order: shared
// token (for API clients/bookmarks), then a valid session cookie, then the
// legacy LAN-trusted bypass (only when neither a token nor login is configured).
func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.AuthToken != "" && tokenFromRequest(r) == s.cfg.AuthToken {
		return true
	}
	if s.cfg.Sessions != nil {
		if _, ok := s.cfg.Sessions.UserFromRequest(r); ok {
			return true
		}
	}
	// Nothing configured to enforce → LAN-trusted (the MVP default).
	return s.cfg.AuthToken == "" && !s.cfg.RequireLogin
}

// auth gates a handler. Unauthenticated HTML navigations are redirected to the
// login page; API/module requests get a plain 401 so XHR callers can react.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authorized(r) {
			next(w, r)
			return
		}
		if s.cfg.RequireLogin && wantsHTML(r) {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// ── login flow ─────────────────────────────────────────────────────────────

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// If login isn't enforced, there's nothing to log into.
	if !s.cfg.RequireLogin || s.cfg.Sessions == nil || s.cfg.Verifier == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	next := safeNext(r.FormValue("next"))

	if r.Method != http.MethodPost {
		// Already logged in? Skip the form.
		if _, ok := s.cfg.Sessions.UserFromRequest(r); ok {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		s.renderLogin(w, "", next, http.StatusOK)
		return
	}

	ip := clientIP(r)
	if s.cfg.Limiter != nil && !s.cfg.Limiter.Allowed(ip) {
		s.renderLogin(w, "Too many attempts — wait a moment and try again.", next, http.StatusTooManyRequests)
		return
	}

	user := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	ok, err := s.cfg.Verifier.Verify(user, pass)
	if err != nil {
		log.Printf("login: auth backend error: %v", err)
		s.renderLogin(w, "Authentication service unavailable.", next, http.StatusBadGateway)
		return
	}
	if !ok {
		if s.cfg.Limiter != nil {
			s.cfg.Limiter.Fail(ip)
		}
		log.Printf("login: failed for user=%q from %s", user, ip)
		s.renderLogin(w, "Invalid username or password.", next, http.StatusUnauthorized)
		return
	}
	if s.cfg.Limiter != nil {
		s.cfg.Limiter.Reset(ip)
	}
	s.cfg.Sessions.SetCookie(w, user, s.cfg.SecureCookie)
	log.Printf("login: ok for user=%q from %s", user, ip)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearCookie(w, s.cfg.SecureCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	user := ""
	if s.cfg.Sessions != nil {
		user, _ = s.cfg.Sessions.UserFromRequest(r)
	}
	writeJSON(w, map[string]any{"user": user, "login_required": s.cfg.RequireLogin})
}

func (s *Server) renderLogin(w http.ResponseWriter, errMsg, next string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	errBlock := ""
	if errMsg != "" {
		errBlock = `<p class="err">` + htmlEscape(errMsg) + `</p>`
	}
	page := strings.NewReplacer(
		"{{ERR}}", errBlock,
		"{{NEXT}}", htmlEscape(next),
	).Replace(loginHTML)
	w.Write([]byte(page))
}

// ── core handlers ──────────────────────────────────────────────────────────

func (s *Server) handleSysinfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, sysinfo.Snapshot())
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.cfg.Modules.Describe())
}

func (s *Server) handleGPS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, gps.PollDetail(1500*time.Millisecond))
}

func (s *Server) handleAdapter(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	d, ok := sysinfo.Detail(name)
	if !ok {
		http.Error(w, "no such interface", http.StatusNotFound)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	jpg, err := fb.SnapshotJPEG(fb.FindLCD(), 80)
	if err != nil {
		http.Error(w, "display unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(jpg)
}

func (s *Server) Run(ctx context.Context) error {
	hs := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sd, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sd)
	}()
	log.Printf("listening on %s", s.cfg.Listen)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────

func tokenFromRequest(r *http.Request) string {
	if t := r.URL.Query().Get("t"); t != "" {
		return t
	}
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}

// wantsHTML distinguishes a browser navigation (redirect to login) from an
// API/XHR/module call (return 401). Anything under /api/ or /m/ is treated as
// API; otherwise we honor the Accept header.
func wantsHTML(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/m/") {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// safeNext sanitizes a post-login redirect target to a local path, preventing
// open-redirects (must start with a single "/").
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func htmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
	).Replace(s)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// loginHTML is a self-contained login page (no external CSS/JS, so it needs no
// gated static assets). {{ERR}} and {{NEXT}} are substituted at render time.
const loginHTML = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>czconsole — sign in</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;
  background:#0c0d10;color:#e6e7ea;font:16px/1.4 system-ui,-apple-system,sans-serif}
.card{width:min(92vw,340px);background:#16181d;border:1px solid #23262d;
  border-radius:16px;padding:28px 24px;box-shadow:0 8px 40px #0008}
h1{margin:0 0 4px;font-size:20px;letter-spacing:.5px}
.sub{margin:0 0 20px;color:#8b8f98;font-size:13px}
label{display:block;font-size:12px;color:#9aa0aa;margin:14px 0 6px}
input{width:100%;padding:11px 12px;border-radius:10px;border:1px solid #2a2e36;
  background:#0e1014;color:#e6e7ea;font-size:16px}
input:focus{outline:none;border-color:#ff8a5a}
button{width:100%;margin-top:22px;padding:12px;border:0;border-radius:10px;
  background:#ff8a5a;color:#16181d;font-size:15px;font-weight:600;cursor:pointer}
button:active{transform:translateY(1px)}
.err{margin:14px 0 0;padding:10px 12px;border-radius:8px;background:#3a1d1d;
  border:1px solid #5c2b2b;color:#ffb4a8;font-size:13px}
</style></head>
<body><form class="card" method="post" action="/login">
<h1>czconsole</h1>
<p class="sub">Sign in with your device account.</p>
<input type="hidden" name="next" value="{{NEXT}}">
<label for="u">Username</label>
<input id="u" name="username" autocomplete="username" autocapitalize="off"
  autocorrect="off" spellcheck="false" required autofocus>
<label for="p">Password</label>
<input id="p" name="password" type="password" autocomplete="current-password" required>
<button type="submit">Sign in</button>
{{ERR}}
</form></body></html>`
