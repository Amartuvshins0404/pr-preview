package web

import (
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/Amartuvshins0404/pr-preview/internal/config"
	"github.com/Amartuvshins0404/pr-preview/internal/manager"
	"github.com/Amartuvshins0404/pr-preview/internal/slug"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

type Server struct {
	cfg   config.Config
	m     *manager.Manager
	gate  *gate
	proxy *httputil.ReverseProxy
}

func New(cfg config.Config, m *manager.Manager) (*Server, error) {
	g, err := newGate(cfg.AccessPassword, cfg.BaseDomain)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, m: m, gate: g}
	s.proxy = s.newProxy()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch {
	case host == s.cfg.BaseDomain:
		s.service(w, r)
	case strings.HasSuffix(host, "."+s.cfg.BaseDomain):
		s.subdomain(w, r, strings.TrimSuffix(host, "."+s.cfg.BaseDomain))
	case r.URL.Path == "/_/tls-ask":
		s.tlsAsk(w, r)
	case r.URL.Path == "/_/health":
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		s.notFound(w, r)
	}
}

func (s *Server) service(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/":
		s.dashboard(w, r)
	case path == "/_/health":
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case path == "/robots.txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	case path == "/_/login":
		s.login(w, r)
	case path == "/_/tls-ask":
		s.tlsAsk(w, r)
	case path == "/_/api/previews":
		s.apiList(w, r)
	case strings.HasPrefix(path, "/_/api/previews/"):
		s.apiPreview(w, r, strings.TrimPrefix(path, "/_/api/previews/"))
	case strings.HasPrefix(path, "/_/"):
		s.notFound(w, r)
	default:
		s.entry(w, r)
	}
}

func (s *Server) tlsAsk(w http.ResponseWriter, r *http.Request) {
	d := strings.ToLower(r.URL.Query().Get("domain"))
	ok := d == s.cfg.BaseDomain
	if sub, found := strings.CutSuffix(d, "."+s.cfg.BaseDomain); found && !strings.Contains(sub, ".") {
		_, ok = s.m.Store().BySlug(sub)
	}
	if !ok {
		http.Error(w, "unknown domain", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) visible(r *http.Request, p state.Preview) bool {
	return !p.Private || s.gate.authed(r)
}

func (s *Server) open(r *http.Request, p state.Preview) bool { return p.Public || s.visible(r, p) }

func (s *Server) canWake(r *http.Request, p state.Preview) bool { return p.Public || s.canStart(r) }

func (s *Server) entry(w http.ResponseWriter, r *http.Request) {
	p, rest, ok := s.findEntry(r.URL.Path)
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.open(r, p) {
		s.needLogin(w, r)
		return
	}
	if p.Serving() {
		target := "https://" + s.cfg.Host(p.Slug) + rest
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		noindex(w)
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if s.canWake(r, p) {
		s.m.Wake(p.ID)
		p, _ = s.m.Store().Get(p.ID)
	}
	s.status(w, r, p, false, http.StatusServiceUnavailable)
}

func (s *Server) findEntry(path string) (state.Preview, string, bool) {
	segs := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 4)
	rest := func(n int) string {
		r := strings.TrimPrefix(path, "/"+strings.Join(segs[:n], "/"))
		if r == "" {
			return "/"
		}
		return r
	}
	if len(segs) >= 2 {
		if n, err := strconv.Atoi(segs[1]); err == nil {
			if p, ok := s.byRepo(segs[0], n); ok {
				return p, rest(2), true
			}
		}
	}
	if len(segs) >= 3 {
		if n, err := strconv.Atoi(segs[2]); err == nil {
			if p, ok := s.m.Store().Get(manager.ID(segs[0], segs[1], n)); ok {
				return p, rest(3), true
			}
		}
	}
	return state.Preview{}, "", false
}

func (s *Server) byRepo(repo string, n int) (state.Preview, bool) {
	var found []state.Preview
	for _, p := range s.m.Store().List() {
		if p.Number == n && strings.EqualFold(p.Repo, repo) {
			found = append(found, p)
		}
	}
	for _, p := range found {
		if p.Slug == slug.Make(p.Repo, n) {
			return p, true
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return state.Preview{}, false
}

func (s *Server) subdomain(w http.ResponseWriter, r *http.Request, sl string) {
	p, ok := s.m.Store().BySlug(sl)
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.open(r, p) {
		s.needLogin(w, r)
		return
	}
	if p.Serving() {
		s.m.Touch(p.ID)
		s.serveProxy(w, r, p)
		return
	}
	if s.canWake(r, p) {
		s.m.Wake(p.ID)
		p, _ = s.m.Store().Get(p.ID)
	}
	s.status(w, r, p, true, http.StatusServiceUnavailable)
}

func (s *Server) needLogin(w http.ResponseWriter, r *http.Request) {
	noindex(w)
	if !wantsHTML(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in at " + s.cfg.Origin() + "/_/login"})
		return
	}
	next := "https://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, s.cfg.Origin()+"/_/login?next="+urlQueryEscape(next), http.StatusFound)
}

func (s *Server) canPost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if s.gate.enabled {
		return origin == s.cfg.Origin() && s.gate.authed(r)
	}
	return origin == "" || origin == s.cfg.Origin()
}

func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func noindex(w http.ResponseWriter) { w.Header().Set("X-Robots-Tag", "noindex") }
