package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Phase string

const (
	Absent   Phase = "absent"
	Queued   Phase = "queued"
	Building Phase = "building"
	Starting Phase = "starting"
	Ready    Phase = "ready"
	Sleeping Phase = "sleeping"
	Failed   Phase = "failed"
)

type Preview struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Owner        string    `json:"owner"`
	Repo         string    `json:"repo"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	Private      bool      `json:"private"`
	Fork         bool      `json:"fork"`
	HeadSHA      string    `json:"headSha"`
	BuiltSHA     string    `json:"builtSha,omitempty"`
	FailedSHA    string    `json:"failedSha,omitempty"`
	Phase        Phase     `json:"phase"`
	Live         bool      `json:"live"`
	Error        string    `json:"error,omitempty"`
	Recipe       string    `json:"recipe,omitempty"`
	Service      string    `json:"service,omitempty"`
	Port         int       `json:"port,omitempty"`
	LastUsed     time.Time `json:"lastUsed"`
	UpdatedAt    time.Time `json:"updatedAt"`
	DeploymentID int64     `json:"deploymentId,omitempty"`
	BaseRef      string    `json:"baseRef,omitempty"`
	Note         string    `json:"note,omitempty"`
	CommentID    int64     `json:"commentId,omitempty"`
}

func (p Preview) Serving() bool {
	switch p.Phase {
	case Ready:
		return true
	case Queued, Building:
		return p.Live
	}
	return false
}

func (p Preview) Running() bool {
	return p.Live || p.Phase == Ready || p.Phase == Starting
}

type file struct {
	Previews []Preview `json:"previews"`
}

type Store struct {
	saveMu   sync.Mutex
	mu       sync.Mutex
	path     string
	previews map[string]*Preview
	bySlug   map[string]string
	dirty    bool
}

func Open(dir string) (*Store, bool, error) {
	s := &Store{path: filepath.Join(dir, "state.json"), previews: map[string]*Preview{}, bySlug: map[string]string{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, false, err
	}
	for i := range f.Previews {
		p := f.Previews[i]
		s.previews[p.ID] = &p
		s.bySlug[p.Slug] = p.ID
	}
	return s, true, nil
}

func (s *Store) Get(id string) (Preview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.previews[id]
	if !ok {
		return Preview{}, false
	}
	return *p, true
}

func (s *Store) BySlug(slug string) (Preview, bool) {
	s.mu.Lock()
	id, ok := s.bySlug[slug]
	s.mu.Unlock()
	if !ok {
		return Preview{}, false
	}
	return s.Get(id)
}

func (s *Store) List() []Preview {
	s.mu.Lock()
	out := make([]Preview, 0, len(s.previews))
	for _, p := range s.previews {
		out = append(out, *p)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner+"/"+out[i].Repo != out[j].Owner+"/"+out[j].Repo {
			return out[i].Owner+"/"+out[i].Repo < out[j].Owner+"/"+out[j].Repo
		}
		return out[i].Number > out[j].Number
	})
	return out
}

func (s *Store) Put(p Preview) error {
	s.mu.Lock()
	if old, ok := s.previews[p.ID]; ok && old.Slug != p.Slug {
		delete(s.bySlug, old.Slug)
	}
	p.UpdatedAt = time.Now().UTC()
	s.previews[p.ID] = &p
	s.bySlug[p.Slug] = p.ID
	s.mu.Unlock()
	return s.Save()
}

func (s *Store) Update(id string, fn func(*Preview)) (Preview, bool, error) {
	s.mu.Lock()
	p, ok := s.previews[id]
	if !ok {
		s.mu.Unlock()
		return Preview{}, false, nil
	}
	fn(p)
	p.UpdatedAt = time.Now().UTC()
	out := *p
	s.mu.Unlock()
	return out, true, s.Save()
}

func (s *Store) Touch(id string) {
	s.mu.Lock()
	if p, ok := s.previews[id]; ok {
		p.LastUsed = time.Now().UTC()
		s.dirty = true
	}
	s.mu.Unlock()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	if p, ok := s.previews[id]; ok {
		delete(s.bySlug, p.Slug)
		delete(s.previews, id)
	}
	s.mu.Unlock()
	return s.Save()
}

func (s *Store) Flush() error {
	s.mu.Lock()
	dirty := s.dirty
	s.mu.Unlock()
	if !dirty {
		return nil
	}
	return s.Save()
}

func (s *Store) Save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	f := file{Previews: make([]Preview, 0, len(s.previews))}
	for _, p := range s.previews {
		f.Previews = append(f.Previews, *p)
	}
	s.dirty = false
	s.mu.Unlock()
	sort.Slice(f.Previews, func(i, j int) bool { return f.Previews[i].ID < f.Previews[j].ID })
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
