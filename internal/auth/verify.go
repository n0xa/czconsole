// Package auth provides czconsole's optional login layer. It is pure Go
// (CGO_ENABLED=0) and never links libpam: real PAM stays in a separate
// privileged agent process that this package talks to over a unix socket, the
// same privsep split czconsole uses for files and kismet.
//
//	web worker (this pkg, deprivileged) --unix socket--> auth agent (root) --exec--> pamtester
//
// The worker (which parses untrusted input) never holds privilege or touches
// /etc/shadow; the agent (which holds privilege) only ever receives a fixed
// {user,password} struct and answers allow/deny. IPC carries intentions, not
// mechanisms.
package auth

import (
	"bufio"
	"encoding/json"
	"net"
	"time"
)

// Request is what the worker sends the agent: a credential pair to verify.
type Request struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// Response is the agent's verdict. Error is for operational faults (PAM/exec
// problems), never a reason string for a bad password — a wrong credential is
// simply OK=false so we leak nothing to the caller.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Verifier is the worker-side client. It is safe for concurrent use; each call
// opens its own short-lived connection to the agent socket.
type Verifier struct {
	Sock    string        // unix socket the auth agent listens on
	Timeout time.Duration // overall budget for a verification (PAM can block)
}

// Verify asks the agent whether user/password authenticates. A false result
// with a nil error means "bad credentials"; a non-nil error means the agent
// could not be reached or failed internally (treat as deny, but distinct for
// logging).
func (v *Verifier) Verify(user, password string) (bool, error) {
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	conn, err := net.DialTimeout("unix", v.Sock, 3*time.Second)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if err := json.NewEncoder(conn).Encode(Request{User: user, Password: password}); err != nil {
		return false, err
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return false, err
	}
	if resp.Error != "" {
		return false, errString(resp.Error)
	}
	return resp.OK, nil
}

type errString string

func (e errString) Error() string { return string(e) }
