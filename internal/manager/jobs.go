package manager

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/github"
	"github.com/Amartuvshins0404/pr-preview/internal/run"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

const probeTimeout = 5 * time.Minute

type logFile struct {
	io.Writer
	f *os.File
}

func (l logFile) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}

func (m *Manager) openLog(slug string, truncate bool) logFile {
	path := m.b.LogPath(slug)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Error("log dir", "slug", slug, "err", err)
		return logFile{Writer: io.Discard}
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if truncate {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		slog.Error("open log", "slug", slug, "err", err)
		return logFile{Writer: io.Discard}
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + m.cfg.GitHubToken))
	return logFile{Writer: run.NewRedactor(f, m.cfg.GitHubToken, basic, m.cfg.AccessPassword), f: f}
}

func (m *Manager) build(ctx context.Context, id string) {
	unlock := m.lock(id)
	defer unlock()
	p, ok := m.store.Get(id)
	if !ok {
		return
	}
	bctx, cancel := context.WithTimeout(ctx, m.cfg.BuildTimeout)
	m.setCancel(id, cancel)
	defer func() {
		m.setCancel(id, nil)
		cancel()
	}()
	log := m.openLog(p.Slug, true)
	defer log.Close()
	fmt.Fprintf(log, "build %s/%s#%d at %s\n", p.Owner, p.Repo, p.Number, time.Now().UTC().Format(time.RFC3339))
	p = m.update(id, func(p *state.Preview) { p.Phase, p.Error = state.Building, "" })
	slog.Info("build started", "slug", p.Slug, "sha", p.HeadSHA)
	go m.b.WarmTLS(p.Slug)
	m.reportStart(ctx, id)
	m.makeRoom(ctx, id)
	started := time.Now()
	res, err := m.b.Build(bctx, project(p), log)
	if err == nil {
		p = m.update(id, func(p *state.Preview) {
			p.Phase, p.Recipe, p.Service, p.Port, p.Note, p.Public = state.Starting, res.Recipe, res.Service, res.Port, res.Note, res.Public
		})
		pctx, pcancel := context.WithTimeout(bctx, probeTimeout)
		err = m.b.Probe(pctx, project(p), res.Service, res.Port, log)
		pcancel()
	}
	sha := res.SHA
	if sha == "" {
		sha = p.HeadSHA
	}
	if err != nil && ctx.Err() != nil {
		fmt.Fprintf(log, "\nstopped: service shutting down\n")
		return
	}
	if err != nil {
		m.fail(id, sha, err, log)
		return
	}
	p = m.update(id, func(p *state.Preview) {
		p.Phase, p.Live, p.BuiltSHA, p.FailedSHA, p.LastUsed = state.Ready, true, sha, "", time.Now().UTC()
		if m.q.has(id) {
			p.Phase = state.Queued
		}
	})
	fmt.Fprintf(log, "ready in %s\n", time.Since(started).Round(time.Second))
	slog.Info("build ready", "slug", p.Slug, "sha", sha, "kind", res.Kind, "seconds", int(time.Since(started).Seconds()))
	m.report(ctx, id, github.Success)
}

func (m *Manager) fail(id, sha string, err error, log io.Writer) {
	fmt.Fprintf(log, "\nfailed: %v\n", err)
	p, _ := m.store.Get(id)
	slog.Warn("preview failed", "slug", p.Slug, "sha", sha, "err", err)
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if serr := m.b.Compose(stopCtx, project(p), log, "stop"); serr != nil {
		slog.Warn("stop after failure", "slug", p.Slug, "err", serr)
	}
	m.update(id, func(p *state.Preview) {
		p.Phase, p.Live, p.FailedSHA, p.Error = state.Failed, false, sha, err.Error()
	})
	m.report(context.Background(), id, github.Failure)
}

func (m *Manager) start(ctx context.Context, id string) {
	unlock := m.lock(id)
	p, ok := m.store.Get(id)
	if !ok || p.Phase != state.Starting {
		unlock()
		return
	}
	log := m.openLog(p.Slug, false)
	defer log.Close()
	fmt.Fprintf(log, "\nwake at %s\n", time.Now().UTC().Format(time.RFC3339))
	m.makeRoom(ctx, id)
	sctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	err := m.b.Compose(sctx, project(p), log, "start")
	if err != nil {
		fmt.Fprintf(log, "start failed, rebuilding: %v\n", err)
		m.update(id, func(p *state.Preview) { p.Phase = state.Absent })
		unlock()
		m.enqueue(id, visit)
		return
	}
	defer unlock()
	if err := m.b.Join(sctx, p.Slug); err != nil {
		fmt.Fprintf(log, "join preview network: %v\n", err)
	}
	if err := m.b.Probe(sctx, project(p), p.Service, p.Port, log); err != nil {
		m.fail(id, p.BuiltSHA, err, log)
		return
	}
	m.update(id, func(p *state.Preview) { p.Phase, p.Live, p.LastUsed = state.Ready, true, time.Now().UTC() })
	slog.Info("preview woke", "slug", p.Slug)
}

func (m *Manager) stop(ctx context.Context, id, reason string) {
	unlock := m.lock(id)
	defer unlock()
	p, ok := m.store.Get(id)
	if !ok || !p.Running() || p.Phase == state.Building || p.Phase == state.Starting {
		return
	}
	log := m.openLog(p.Slug, false)
	defer log.Close()
	fmt.Fprintf(log, "\nstop (%s) at %s\n", reason, time.Now().UTC().Format(time.RFC3339))
	if err := m.b.Compose(ctx, project(p), log, "stop"); err != nil {
		slog.Warn("stop failed", "slug", p.Slug, "err", err)
		return
	}
	m.update(id, func(p *state.Preview) {
		p.Live = false
		if p.Phase == state.Ready {
			p.Phase = state.Sleeping
		}
	})
	slog.Info("preview stopped", "slug", p.Slug, "reason", reason)
}

func (m *Manager) makeRoom(ctx context.Context, keep string) {
	for {
		var running int
		var victim *state.Preview
		for _, p := range m.store.List() {
			if p.ID == keep || !p.Running() {
				continue
			}
			running++
			if p.Phase == state.Ready && (victim == nil || p.LastUsed.Before(victim.LastUsed)) {
				v := p
				victim = &v
			}
		}
		if running < m.cfg.MaxRunning || victim == nil {
			return
		}
		m.stop(ctx, victim.ID, "max running")
	}
}

func (m *Manager) sleepIdle(ctx context.Context) {
	cutoff := time.Now().Add(-m.cfg.Idle)
	for _, p := range m.store.List() {
		if p.Phase == state.Ready && p.LastUsed.Before(cutoff) {
			m.stop(ctx, p.ID, "idle")
		}
	}
}

func (m *Manager) teardown(ctx context.Context, id string) {
	m.cancel(id)
	m.q.remove(id)
	unlock := m.lock(id)
	defer func() {
		unlock()
		m.mu.Lock()
		delete(m.locks, id)
		m.mu.Unlock()
	}()
	p, ok := m.store.Get(id)
	if !ok {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	log := m.openLog(p.Slug, false)
	err := m.b.Down(dctx, project(p), log)
	log.Close()
	if err != nil {
		slog.Warn("teardown down failed", "slug", p.Slug, "err", err)
	}
	if err := os.RemoveAll(m.b.Dir(p.Slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("teardown remove dir", "slug", p.Slug, "err", err)
	}
	m.report(ctx, id, github.Inactive)
	if err := m.store.Delete(id); err != nil {
		slog.Error("save state", "err", err)
	}
	slog.Info("preview removed", "slug", p.Slug)
}
