package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

type previewJSON struct {
	Slug       string      `json:"slug"`
	Owner      string      `json:"owner"`
	Repo       string      `json:"repo"`
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	PRURL      string      `json:"prUrl"`
	EntryURL   string      `json:"entryUrl"`
	PreviewURL string      `json:"previewUrl"`
	Private    bool        `json:"private"`
	Phase      state.Phase `json:"phase"`
	Serving    bool        `json:"serving"`
	HeadSHA    string      `json:"headSha"`
	BuiltSHA   string      `json:"builtSha,omitempty"`
	FailedSHA  string      `json:"failedSha,omitempty"`
	Error      string      `json:"error,omitempty"`
	Recipe     string      `json:"recipe,omitempty"`
	LastUsed   time.Time   `json:"lastUsed"`
	UpdatedAt  time.Time   `json:"updatedAt"`
	Log        *logChunk   `json:"log,omitempty"`
}

func (s *Server) previewJSON(p state.Preview, log *logChunk) previewJSON {
	return previewJSON{
		Slug: p.Slug, Owner: p.Owner, Repo: p.Repo, Number: p.Number, Title: p.Title, PRURL: p.URL,
		EntryURL: s.m.EntryURL(p), PreviewURL: "https://" + s.cfg.Host(p.Slug), Private: p.Private,
		Phase: p.Phase, Serving: p.Serving(), HeadSHA: p.HeadSHA, BuiltSHA: p.BuiltSHA, FailedSHA: p.FailedSHA,
		Error: p.Error, Recipe: p.Recipe, LastUsed: p.LastUsed, UpdatedAt: p.UpdatedAt, Log: log,
	}
}

func (s *Server) apiList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := struct {
		Previews []previewJSON `json:"previews"`
		Hidden   int           `json:"hidden"`
	}{Previews: []previewJSON{}}
	for _, p := range s.m.Store().List() {
		if !s.visible(r, p) {
			out.Hidden++
			continue
		}
		out.Previews = append(out.Previews, s.previewJSON(p, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiPreview(w http.ResponseWriter, r *http.Request, rest string) {
	sl, action, _ := strings.Cut(rest, "/")
	p, ok := s.m.Store().BySlug(sl)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown preview"})
		return
	}
	if !s.visible(r, p) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return
	}
	if action == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		log := readLog(s.m.Builder().LogPath(p.Slug), logOffset(r.URL.Query().Get("offset")))
		writeJSON(w, http.StatusOK, s.previewJSON(p, &log))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.canPost(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "sign in and post from " + s.cfg.Origin()})
		return
	}
	switch action {
	case "rebuild":
		s.m.Rebuild(p.ID)
	case "stop":
		s.m.Stop(p.ID)
	case "reset":
		s.m.Reset(p.ID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"ok": action})
}
