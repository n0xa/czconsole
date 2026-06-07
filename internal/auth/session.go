package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CookieName is the session cookie set after a successful login.
const CookieName = "czsession"

// Sessions issues and validates stateless, HMAC-signed session tokens. There is
// no server-side store: a token is self-describing (user + expiry) and trusted
// only if its signature verifies under a per-process random secret. Restarting
// czconsole rotates the secret and thus logs everyone out — fine for a field
// device, and it means a stolen disk image yields no usable sessions.
type Sessions struct {
	secret []byte
	ttl    time.Duration
}

// NewSessions creates a session signer with a fresh random secret. ttl is how
// long an issued token stays valid; <=0 defaults to 12h.
func NewSessions(ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// rand.Read never fails on Linux, but never run with a guessable key.
		panic("auth: cannot read CSPRNG: " + err.Error())
	}
	return &Sessions{secret: secret, ttl: ttl}
}

// token format: base64url(user) "." expiryUnix "." base64url(hmac)
func (s *Sessions) sign(user string, exp int64) string {
	body := base64.RawURLEncoding.EncodeToString([]byte(user)) + "." + strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Issue returns a signed token for user, valid for the configured ttl.
func (s *Sessions) Issue(user string) string {
	return s.sign(user, time.Now().Add(s.ttl).Unix())
}

// Validate returns the token's user if the signature verifies and it has not
// expired. A constant-time MAC compare avoids signature-timing oracles.
func (s *Sessions) Validate(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expected)) != 1 {
		return "", false
	}
	userBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	return string(userBytes), true
}

// SetCookie writes the session token as a hardened cookie. secure should be true
// when served over TLS so the cookie is never sent in clear.
func (s *Sessions) SetCookie(w http.ResponseWriter, user string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    s.Issue(user),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.ttl.Seconds()),
	})
}

// ClearCookie expires the session cookie (logout).
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// UserFromRequest returns the authenticated user from a valid session cookie.
func (s *Sessions) UserFromRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	return s.Validate(c.Value)
}
