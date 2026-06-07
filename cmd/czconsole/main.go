// Command czconsole is the Cardputer Zero field console.
//
// It runs in one of two modes (see the privsep design in the README):
//
//   - web mode (default): the network-facing worker. Serves the dashboard, the
//     LCD mirror, telemetry, and the module host. Meant to run as the dedicated
//     unprivileged _czconsole user.
//   - files-agent mode (--files-agent): a tiny helper that serves only the
//     Files module over a unix socket, run as the operator (kali/pi) so file
//     I/O has correct ownership. The web worker reverse-proxies to it.
//   - auth-agent mode (--auth-agent): the privileged PAM verifier. Runs as root
//     over a unix socket and execs pamtester; the only component that can reach
//     /etc/shadow. The web worker asks it yes/no and never holds privilege.
//
// Build (pure Go, no cgo — cross-compiles from any host):
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o czconsole ./cmd/czconsole
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/n0xa/czconsole/internal/auth"
	"github.com/n0xa/czconsole/internal/modules"
	"github.com/n0xa/czconsole/internal/server"
)

func main() {
	var (
		listen     = flag.String("listen", ":8080", "web mode: address to listen on")
		authToken  = flag.String("auth-token", envOr("CZCONSOLE_TOKEN", ""), "web mode: shared token (empty = LAN-trusted)")
		modulesDir = flag.String("modules-dir", "/etc/czconsole/modules.d", "web mode: external manifest modules dir")
		filesSock  = flag.String("files-sock", "/run/czconsole/files.sock", "unix socket bridging worker and files-agent")

		filesAgent = flag.Bool("files-agent", false, "run ONLY the Files module on --files-sock, as the operator")
		filesRoot  = flag.String("files-root", "", "files-agent mode: home dir to expose (empty = autodetect)")

		authAgent    = flag.Bool("auth-agent", false, "run ONLY the privileged PAM verifier on --auth-sock")
		authSock     = flag.String("auth-sock", "/run/czconsole/auth.sock", "unix socket bridging worker and auth-agent")
		pamService   = flag.String("pam-service", "czconsole", "auth-agent mode: PAM service name (/etc/pam.d/<name>)")
		requireLogin = flag.Bool("require-login", false, "web mode: require a PAM login (needs the auth-agent)")
		secureCookie = flag.Bool("secure-cookie", false, "web mode: set the session cookie Secure flag (TLS only)")
		sessionTTL   = flag.Int("session-ttl-min", 720, "web mode: session lifetime in minutes")
	)
	flag.Parse()

	if *filesAgent {
		runFilesAgent(*filesSock, *filesRoot)
		return
	}

	if *authAgent {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := auth.ServeAgent(ctx, *authSock, *pamService); err != nil {
			log.Fatalf("auth-agent: %v", err)
		}
		return
	}

	reg := modules.NewRegistry(*modulesDir, *filesSock)
	if err := reg.Load(); err != nil {
		log.Printf("module load: %v (continuing with bundled only)", err)
	}

	cfg := server.Config{Listen: *listen, AuthToken: *authToken, Modules: reg}
	if *requireLogin {
		cfg.RequireLogin = true
		cfg.Sessions = auth.NewSessions(time.Duration(*sessionTTL) * time.Minute)
		cfg.Verifier = &auth.Verifier{Sock: *authSock}
		// Front-line throttle in front of pam_faillock: 5 failures per IP per
		// 5 minutes, then a 5-minute lockout.
		cfg.Limiter = auth.NewRateLimiter(5, 5*time.Minute, 5*time.Minute)
		cfg.SecureCookie = *secureCookie
		log.Printf("login required (PAM service via %s)", *authSock)
	}

	srv := server.New(cfg)
	for _, u := range lanURLs(*listen) {
		log.Printf("Field Console → %s", u)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// runFilesAgent serves the real Files module on a unix socket. The socket is the
// privilege boundary (only the worker's group can connect), so handlers run
// without the token auth — pass an identity middleware.
func runFilesAgent(sock, root string) {
	if root == "" {
		root = modules.DefaultFilesRoot()
	}
	log.Printf("files-agent: root=%s sock=%s", root, sock)

	mux := http.NewServeMux()
	identity := func(h http.HandlerFunc) http.HandlerFunc { return h }
	modules.NewFilesModule(root).Mount(mux, "/m/files/", identity)

	_ = os.Remove(sock) // clear a stale socket from a crashed run
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("files-agent listen: %v", err)
	}
	// The socket is created owned by the agent's primary group (kali); chgrp it
	// to the shared 'czconsole' group and 0660 so the worker — and only the
	// worker — can connect. (Owner is in the czconsole group, so chgrp is
	// permitted without privilege.)
	if g, err := user.LookupGroup("czconsole"); err == nil {
		if gid, err := strconv.Atoi(g.Gid); err == nil {
			if err := os.Chown(sock, -1, gid); err != nil {
				log.Printf("files-agent chgrp sock: %v", err)
			}
		}
	}
	if err := os.Chmod(sock, 0o660); err != nil {
		log.Printf("files-agent chmod sock: %v", err)
	}

	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); hs.Close(); os.Remove(sock) }()
	if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("files-agent serve: %v", err)
	}
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// lanURLs enumerates the non-loopback IPv4 addresses the console is reachable
// at, so the caller can print them (and APPLaunch can show one on the LCD).
func lanURLs(listen string) []string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "8080"
	}
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			out = append(out, "http://"+ip4.String()+":"+port)
		}
	}
	if len(out) == 0 {
		out = append(out, "http://<device-ip>:"+port)
	}
	return out
}
