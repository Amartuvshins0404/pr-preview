package manager

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/build"
	"github.com/Amartuvshins0404/pr-preview/internal/config"
	"github.com/Amartuvshins0404/pr-preview/internal/github"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

type Manager struct {
	cfg      config.Config
	store    *state.Store
	gh       *github.Client
	b        *build.Builder
	q        *queue
	baseline bool

	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	cancels map[string]context.CancelFunc
}

func New(cfg config.Config, store *state.Store, hadState bool, gh *github.Client, b *build.Builder) *Manager {
	return &Manager{
		cfg:      cfg,
		store:    store,
		gh:       gh,
		b:        b,
		q:        newQueue(),
		baseline: !hadState,
		locks:    map[string]*sync.Mutex{},
		cancels:  map[string]context.CancelFunc{},
	}
}

func (m *Manager) Store() *state.Store { return m.store }

func (m *Manager) Builder() *build.Builder { return m.b }

func (m *Manager) Run(ctx context.Context) {
	m.reconcile(ctx)
	for i := 0; i < m.cfg.BuildConcurrency; i++ {
		go m.worker(ctx)
	}
	m.poll(ctx)
	poll := time.NewTicker(m.cfg.Poll)
	idle := time.NewTicker(time.Minute)
	prune := time.NewTicker(6 * time.Hour)
	defer poll.Stop()
	defer idle.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := m.store.Save(); err != nil {
				slog.Error("save state", "err", err)
			}
			return
		case <-poll.C:
			m.poll(ctx)
		case <-idle.C:
			m.sleepIdle(ctx)
			if err := m.store.Flush(); err != nil {
				slog.Error("save state", "err", err)
			}
		case <-prune.C:
			m.b.Prune(ctx)
		}
	}
}

func (m *Manager) worker(ctx context.Context) {
	for {
		id, ok := m.q.pop(ctx)
		if !ok {
			return
		}
		m.build(ctx, id)
	}
}

func (m *Manager) lock(id string) func() {
	m.mu.Lock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) setCancel(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	if cancel == nil {
		delete(m.cancels, id)
	} else {
		m.cancels[id] = cancel
	}
	m.mu.Unlock()
}

func (m *Manager) cancel(id string) {
	m.mu.Lock()
	c := m.cancels[id]
	m.mu.Unlock()
	if c != nil {
		c()
	}
}

func (m *Manager) Touch(id string) { m.store.Touch(id) }

func (m *Manager) Wake(id string) {
	start := false
	_, _, err := m.store.Update(id, func(p *state.Preview) {
		switch {
		case p.Phase == state.Sleeping:
			p.Phase = state.Starting
			start = true
		case p.Phase == state.Absent, p.Phase == state.Failed && p.FailedSHA != p.HeadSHA:
			p.Phase = state.Queued
		}
	})
	if err != nil {
		slog.Error("save state", "err", err)
	}
	p, ok := m.store.Get(id)
	if !ok {
		return
	}
	switch {
	case start:
		go m.start(context.Background(), id)
	case p.Phase == state.Queued:
		m.q.push(id, visit)
	}
}

func (m *Manager) Rebuild(id string) { m.enqueue(id, visit) }

func (m *Manager) Stop(id string) { go m.stop(context.Background(), id, "requested") }

func (m *Manager) Reset(id string) {
	go func() {
		m.cancel(id)
		m.q.remove(id)
		unlock := m.lock(id)
		if p, ok := m.store.Get(id); ok {
			log := m.openLog(p.Slug, true)
			if err := m.b.Down(context.Background(), project(p), log); err != nil {
				slog.Warn("reset down failed", "slug", p.Slug, "err", err)
			}
			log.Close()
			m.update(id, func(p *state.Preview) { p.Phase, p.Live, p.FailedSHA = state.Absent, false, "" })
		}
		unlock()
		m.enqueue(id, visit)
	}()
}

func (m *Manager) enqueue(id string, prio int) {
	m.update(id, func(p *state.Preview) {
		switch p.Phase {
		case state.Building, state.Starting:
		default:
			p.Phase = state.Queued
		}
	})
	m.q.push(id, prio)
}

func (m *Manager) update(id string, fn func(*state.Preview)) state.Preview {
	p, _, err := m.store.Update(id, fn)
	if err != nil {
		slog.Error("save state", "err", err)
	}
	return p
}

func project(p state.Preview) build.Project {
	return build.Project{Slug: p.Slug, Owner: p.Owner, Repo: p.Repo, Number: p.Number, SHA: p.BuiltSHA, Recipe: p.Recipe, BaseRef: p.BaseRef}
}
