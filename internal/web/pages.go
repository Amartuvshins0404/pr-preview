package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

//go:embed templates/*.html
var templateFS embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"short": short,
	"ago":   ago,
}).ParseFS(templateFS, "templates/*.html"))

const logTail = 16 << 10

func render(w http.ResponseWriter, code int, name string, data any) {
	noindex(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("render", "template", name, "err", err)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	noindex(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json", "err", err)
	}
}

type logChunk struct {
	Offset int64  `json:"offset"`
	Text   string `json:"text"`
}

func readLog(path string, offset int64) logChunk {
	f, err := os.Open(path)
	if err != nil {
		return logChunk{}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return logChunk{}
	}
	size := st.Size()
	if offset < 0 || offset > size {
		offset = max(0, size-logTail)
	}
	b, err := io.ReadAll(io.LimitReader(io.NewSectionReader(f, offset, size-offset), 256<<10))
	if err != nil {
		return logChunk{Offset: offset}
	}
	return logChunk{Offset: offset + int64(len(b)), Text: string(b)}
}

func logOffset(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

type statusView struct {
	P        state.Preview
	Entry    string
	Sub      bool
	CanPost  bool
	Log      logChunk
	Failed   bool
	Progress bool
	Login    string
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, p state.Preview, sub bool, code int) {
	showLog := s.visible(r, p)
	var log logChunk
	if showLog {
		log = readLog(s.m.Builder().LogPath(p.Slug), -1)
	}
	if !wantsHTML(r) {
		w.Header().Set("Retry-After", "5")
		w.Header().Set("X-PR-Preview", "status")
		if sub && showLog {
			log = readLog(s.m.Builder().LogPath(p.Slug), logOffset(r.Header.Get("X-PR-Preview-Offset")))
		}
		writeJSON(w, code, s.previewJSON(p, &log))
		return
	}
	v := statusView{
		P:        p,
		Entry:    s.m.EntryURL(p),
		Sub:      sub,
		CanPost:  !sub && s.canStart(r),
		Log:      log,
		Failed:   p.Phase == state.Failed,
		Progress: p.Phase == state.Queued || p.Phase == state.Building || p.Phase == state.Starting,
	}
	if !v.Progress && !s.canStart(r) {
		v.Login = s.cfg.Origin() + "/_/login?next=" + urlQueryEscape(v.Entry)
	}
	w.Header().Set("X-PR-Preview", "status")
	render(w, code, "status.html", v)
}

func (s *Server) canStart(r *http.Request) bool { return !s.gate.enabled || s.gate.authed(r) }

type dashRepo struct {
	Name     string
	Previews []state.Preview
}

type liveRow struct {
	P   state.Preview
	URL string
}

type dashView struct {
	Live    []liveRow
	Repos   []dashRepo
	Hidden  int
	Total   int
	CanPost bool
	Gated   bool
	Login   string
	Entry   func(state.Preview) string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v := dashView{CanPost: s.canStart(r), Gated: s.gate.enabled && !s.gate.authed(r), Login: "/_/login?next=/", Entry: s.m.EntryURL}
	idx := map[string]int{}
	for _, p := range s.m.Store().List() {
		v.Total++
		if !s.visible(r, p) {
			v.Hidden++
			continue
		}
		if p.Serving() {
			v.Live = append(v.Live, liveRow{P: p, URL: "https://" + s.cfg.Host(p.Slug)})
		}
		name := p.Owner + "/" + p.Repo
		i, ok := idx[name]
		if !ok {
			i = len(v.Repos)
			idx[name] = i
			v.Repos = append(v.Repos, dashRepo{Name: name})
		}
		v.Repos[i].Previews = append(v.Repos[i].Previews, p)
	}
	render(w, http.StatusOK, "dashboard.html", v)
}

type loginView struct {
	Next  string
	Error string
	Open  bool
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	next := s.gate.safeNext(r.URL.Query().Get("next"))
	if !s.gate.enabled {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	switch r.Method {
	case http.MethodGet:
		render(w, http.StatusOK, "login.html", loginView{Next: next})
	case http.MethodPost:
		if o := r.Header.Get("Origin"); o != "" && o != s.cfg.Origin() {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		next = s.gate.safeNext(r.PostForm.Get("next"))
		if !s.gate.check(clientIP(r), r.PostForm.Get("password")) {
			render(w, http.StatusUnauthorized, "login.html", loginView{Next: next, Error: "Wrong password, or too many tries. Wait and try again."})
			return
		}
		s.gate.setSession(w)
		noindex(w)
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		render(w, http.StatusNotFound, "notfound.html", s.cfg.Origin())
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h ago"
	}
	return strconv.Itoa(int(d.Hours()/24)) + " days ago"
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }
