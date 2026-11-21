package web

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cookieName  = "pr_preview_session"
	sessionTTL  = 14 * 24 * time.Hour
	maxFails    = 5
	failWindow  = 15 * time.Minute
	failPenalty = time.Second
)

type gate struct {
	enabled  bool
	domain   string
	password [32]byte
	key      []byte

	mu    sync.Mutex
	fails map[string]*attempts
}

type attempts struct {
	n     int
	first time.Time
}

func newGate(password, domain string) (*gate, error) {
	g := &gate{enabled: password != "", domain: domain, fails: map[string]*attempts{}}
	if !g.enabled {
		return g, nil
	}
	g.password = sha256.Sum256([]byte(password))
	key, err := pbkdf2.Key(sha256.New, password, []byte("pr-preview session "+domain), 210000, 32)
	if err != nil {
		return nil, err
	}
	g.key = key
	return g, nil
}

func (g *gate) sign(exp int64) string {
	mac := hmac.New(sha256.New, g.key)
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (g *gate) authed(r *http.Request) bool {
	if !g.enabled {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	expStr, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(g.sign(exp)))
}

func (g *gate) setSession(w http.ResponseWriter) {
	exp := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    strconv.FormatInt(exp, 10) + "." + g.sign(exp),
		Path:     "/",
		Domain:   g.domain,
		Expires:  time.Unix(exp, 0),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (g *gate) check(ip, password string) bool {
	g.mu.Lock()
	a := g.fails[ip]
	if a != nil && time.Since(a.first) > failWindow {
		delete(g.fails, ip)
		a = nil
	}
	blocked := a != nil && a.n >= maxFails
	g.mu.Unlock()
	if blocked {
		return false
	}
	sum := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(sum[:], g.password[:]) == 1 {
		g.mu.Lock()
		delete(g.fails, ip)
		g.mu.Unlock()
		return true
	}
	g.mu.Lock()
	if a = g.fails[ip]; a == nil {
		a = &attempts{first: time.Now()}
		g.fails[ip] = a
	}
	a.n++
	g.mu.Unlock()
	time.Sleep(failPenalty)
	return false
}

func (g *gate) safeNext(next string) string {
	if next == "" {
		return "/"
	}
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.HasPrefix(next, "/\\") {
		return next
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return "/"
	}
	h := strings.ToLower(u.Hostname())
	if h == g.domain || strings.HasSuffix(h, "."+g.domain) {
		return u.String()
	}
	return "/"
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func stripSessionCookie(h http.Header) {
	var keep []string
	for _, line := range h.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if strings.TrimSpace(part) != "" && name != cookieName {
				keep = append(keep, strings.TrimSpace(part))
			}
		}
	}
	h.Del("Cookie")
	if len(keep) > 0 {
		h.Set("Cookie", strings.Join(keep, "; "))
	}
}
