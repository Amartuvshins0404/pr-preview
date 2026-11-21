package manager

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/build"
	"github.com/Amartuvshins0404/pr-preview/internal/github"
	"github.com/Amartuvshins0404/pr-preview/internal/slug"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

func ID(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", strings.ToLower(owner), strings.ToLower(repo), number)
}

func (m *Manager) poll(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	prs, err := m.gh.OpenPRs(pctx)
	cancel()
	if err != nil {
		slog.Warn("poll failed", "err", err)
		return
	}
	keys := make([]slug.PR, len(prs))
	for i, pr := range prs {
		keys[i] = slug.PR{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, Fork: pr.Fork}
	}
	slugs := slug.Assign(keys)
	open := map[string]bool{}
	var queued int
	for i, pr := range prs {
		id := ID(pr.Owner, pr.Repo, pr.Number)
		open[id] = true
		old, ok := m.store.Get(id)
		if ok && old.Slug != slugs[i] {
			m.teardown(ctx, id)
			ok = false
		}
		if !ok {
			if err := m.store.Put(newPreview(id, slugs[i], pr)); err != nil {
				slog.Error("save state", "err", err)
			}
			if !m.baseline {
				m.enqueue(id, eager)
				queued++
			}
			continue
		}
		changed := old.HeadSHA != pr.HeadSHA
		m.update(id, func(p *state.Preview) {
			p.Owner, p.Repo, p.Title, p.URL, p.Private, p.Fork = pr.Owner, pr.Repo, pr.Title, pr.URL, pr.Private, pr.Fork
			p.HeadSHA = pr.HeadSHA
		})
		if changed {
			m.enqueue(id, eager)
			queued++
		}
	}
	for _, p := range m.store.List() {
		if !open[p.ID] {
			go m.teardown(ctx, p.ID)
		}
	}
	slog.Info("poll done", "open", len(prs), "queued", queued, "baseline", m.baseline)
	m.baseline = false
}

func newPreview(id, s string, pr github.PR) state.Preview {
	return state.Preview{
		ID: id, Slug: s, Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number, Title: pr.Title, URL: pr.URL,
		Private: pr.Private, Fork: pr.Fork, HeadSHA: pr.HeadSHA, Phase: state.Absent,
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	projects, err := build.ListProjects(ctx)
	if err != nil {
		slog.Error("list compose projects", "err", err)
		return
	}
	status := map[string]string{}
	for _, p := range projects {
		status[p.Name] = p.Status
	}
	known := map[string]bool{}
	var requeue []string
	for _, p := range m.store.List() {
		name := build.ProjectName(p.Slug)
		known[name] = true
		st, has := status[name]
		inFlight := p.Phase == state.Queued || p.Phase == state.Building || p.Phase == state.Starting
		m.update(p.ID, func(p *state.Preview) {
			switch {
			case has && strings.Contains(st, "running"):
				p.Phase, p.Live = state.Ready, true
			case p.Phase == state.Failed:
				p.Live = false
			case has:
				p.Phase, p.Live = state.Sleeping, false
			default:
				p.Phase, p.Live = state.Absent, false
			}
		})
		if inFlight {
			requeue = append(requeue, p.ID)
		}
	}
	for name := range status {
		if known[name] {
			continue
		}
		if m.baseline {
			slog.Warn("unknown preview project left running", "project", name)
			continue
		}
		slog.Info("removing orphan preview project", "project", name)
		orphan := build.Project{Slug: strings.TrimPrefix(name, "pv-")}
		if err := m.b.Down(ctx, orphan, nil); err != nil {
			slog.Warn("orphan down failed", "project", name, "err", err)
		}
	}
	for _, id := range requeue {
		m.enqueue(id, eager)
	}
	slog.Info("reconciled", "previews", len(known), "projects", len(projects), "requeued", len(requeue))
}
