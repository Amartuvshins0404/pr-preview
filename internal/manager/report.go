package manager

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/github"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

func (m *Manager) EntryURL(p state.Preview) string {
	return fmt.Sprintf("%s/%s/%s/%d", m.cfg.Origin(), p.Owner, p.Repo, p.Number)
}

func (m *Manager) reportStart(ctx context.Context, id string) {
	p, ok := m.store.Get(id)
	if !ok || !m.cfg.Reports(p.Owner) {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if p.DeploymentID != 0 {
		if err := m.gh.SetDeploymentStatus(rctx, p.Owner, p.Repo, p.DeploymentID, github.Inactive, ""); err != nil {
			slog.Warn("deployment inactive failed", "slug", p.Slug, "err", err)
		}
	}
	depID, err := m.gh.CreateDeployment(rctx, p.Owner, p.Repo, p.HeadSHA, "Preview of #"+fmt.Sprint(p.Number))
	if err != nil {
		slog.Warn("create deployment failed", "slug", p.Slug, "err", err)
		m.update(id, func(p *state.Preview) { p.DeploymentID = 0 })
		return
	}
	m.update(id, func(p *state.Preview) { p.DeploymentID = depID })
	if err := m.gh.SetDeploymentStatus(rctx, p.Owner, p.Repo, depID, github.InProgress, m.EntryURL(p)); err != nil {
		slog.Warn("deployment status failed", "slug", p.Slug, "err", err)
	}
}

func (m *Manager) report(ctx context.Context, id string, s github.DeployState) {
	p, ok := m.store.Get(id)
	if !ok || p.DeploymentID == 0 || !m.cfg.Reports(p.Owner) {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.gh.SetDeploymentStatus(rctx, p.Owner, p.Repo, p.DeploymentID, s, m.EntryURL(p)); err != nil {
		slog.Warn("deployment status failed", "slug", p.Slug, "state", s, "err", err)
	}
}
