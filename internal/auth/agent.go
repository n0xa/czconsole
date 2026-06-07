package auth

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// ServeAgent runs the privileged PAM verifier. It listens on a unix socket
// (group-restricted to the worker, exactly like the files agent) and, for each
// connection, reads one Request and answers one Response by running the system
// `pamtester` against pamService. This process is the only part of czconsole
// that runs with privilege and the only part that can reach /etc/shadow — and
// it never parses anything richer than {user,password}.
//
// We exec `pamtester` rather than link libpam so the whole binary stays
// CGO_ENABLED=0. pamtester runs the real PAM stack for the named service
// (/etc/pam.d/<service>), so pam_faillock, account checks, and the site's
// actual auth policy all apply.
func ServeAgent(ctx context.Context, sock, pamService string) error {
	if pamService == "" {
		pamService = "czconsole"
	}

	_ = os.Remove(sock) // clear a stale socket from a crashed run
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	chgrpSocket(sock, "czconsole")

	log.Printf("auth-agent: sock=%s pam-service=%s", sock, pamService)

	go func() {
		<-ctx.Done()
		ln.Close()
		os.Remove(sock)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("auth-agent accept: %v", err)
				continue
			}
		}
		go handleConn(conn, pamService)
	}
}

func handleConn(conn net.Conn, pamService string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResp(conn, Response{Error: "bad request"})
		return
	}

	if !validUser(req.User) || req.Password == "" || len(req.Password) > 1024 {
		// Never reveal which constraint failed; just deny.
		writeResp(conn, Response{OK: false})
		return
	}

	ok, agentErr := runPamtester(pamService, req.User, req.Password)
	if agentErr != "" {
		log.Printf("auth-agent: pam error for user=%q: %s", req.User, agentErr)
		writeResp(conn, Response{Error: agentErr})
		return
	}
	writeResp(conn, Response{OK: ok})
}

// runPamtester returns (authenticated, agentError). A non-empty agentError means
// an operational fault (pamtester missing, exec failure) — distinct from a bad
// password, which is simply (false, ""). We deliberately do not surface PAM's
// failure reason to the worker.
func runPamtester(service, user, password string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pamtester", service, user, "authenticate")
	// pamtester's conversation reads the password from stdin when there is no
	// controlling tty (which is the case under systemd). A trailing newline
	// terminates the line read.
	cmd.Stdin = strings.NewReader(password + "\n")
	// Drop the agent's environment; pamtester needs none of it and we don't
	// want to leak anything into the PAM stack's child processes.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}

	err := cmd.Run()
	if err == nil {
		return true, "" // exit 0 → authenticated
	}
	if ctx.Err() == context.DeadlineExceeded {
		return false, "pam timeout"
	}
	if ee, ok := err.(*exec.ExitError); ok {
		// pamtester exited non-zero: authentication failed (or was denied by
		// faillock/account). Treat as a clean deny, not an agent error.
		_ = ee
		return false, ""
	}
	// Could not start pamtester at all (not installed, permission, etc.).
	return false, "pamtester unavailable: " + err.Error()
}

// validUser bounds the username to a conservative POSIX-ish charset. exec runs
// pamtester via argv (no shell), so this is defense-in-depth, not the only
// barrier against injection.
func validUser(u string) bool {
	if u == "" || len(u) > 32 {
		return false
	}
	for _, c := range u {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == '-' || c == '.':
		default:
			return false
		}
	}
	return true
}

func writeResp(conn net.Conn, r Response) {
	_ = json.NewEncoder(conn).Encode(r)
}

// chgrpSocket hands the socket to the shared worker group at 0660 so only the
// web worker can connect — same boundary the files agent uses.
func chgrpSocket(sock, group string) {
	if g, err := user.LookupGroup(group); err == nil {
		if gid, err := strconv.Atoi(g.Gid); err == nil {
			if err := os.Chown(sock, -1, gid); err != nil {
				log.Printf("auth-agent chgrp sock: %v", err)
			}
		}
	}
	if err := os.Chmod(sock, 0o660); err != nil {
		log.Printf("auth-agent chmod sock: %v", err)
	}
}
