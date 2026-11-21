package manager

import (
	"context"
	"sync"
)

const (
	eager = 0
	visit = 1
)

type queue struct {
	mu    sync.Mutex
	wake  chan struct{}
	prio  map[string]int
	order []string
}

func newQueue() *queue {
	return &queue{wake: make(chan struct{}, 1), prio: map[string]int{}}
}

func (q *queue) push(id string, prio int) {
	q.mu.Lock()
	if old, ok := q.prio[id]; ok {
		if prio > old {
			q.prio[id] = prio
		}
	} else {
		q.prio[id] = prio
		q.order = append(q.order, id)
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) remove(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.prio[id]; !ok {
		return
	}
	delete(q.prio, id)
	for i, v := range q.order {
		if v == id {
			q.order = append(q.order[:i], q.order[i+1:]...)
			break
		}
	}
}

func (q *queue) has(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.prio[id]
	return ok
}

func (q *queue) pop(ctx context.Context) (string, bool) {
	for {
		q.mu.Lock()
		best := -1
		for i, id := range q.order {
			if best < 0 || q.prio[id] > q.prio[q.order[best]] {
				best = i
			}
		}
		if best >= 0 {
			id := q.order[best]
			q.order = append(q.order[:best], q.order[best+1:]...)
			delete(q.prio, id)
			more := len(q.order) > 0
			q.mu.Unlock()
			if more {
				select {
				case q.wake <- struct{}{}:
				default:
				}
			}
			return id, true
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", false
		case <-q.wake:
		}
	}
}
