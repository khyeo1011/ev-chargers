// Package memory provides in-memory stand-ins for the AWS-backed store and
// publisher. They let you run the API with zero AWS setup and they power the
// unit tests.
package memory

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/khyeo1011/ev-chargers/internal/charger"
)

// Store is a thread-safe map-backed charger.Store.
type Store struct {
	mu   sync.RWMutex
	data map[string]charger.Charger
}

func NewStore() *Store { return &Store{data: map[string]charger.Charger{}} }

func (s *Store) Put(_ context.Context, c charger.Charger) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[c.ID] = c
	return nil
}

func (s *Store) Get(_ context.Context, id string) (charger.Charger, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[id]
	if !ok {
		return charger.Charger{}, charger.ErrNotFound
	}
	return c, nil
}

func (s *Store) List(_ context.Context) ([]charger.Charger, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]charger.Charger, 0, len(s.data))
	for _, c := range s.data {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Publisher records events in memory and logs them.
type Publisher struct {
	mu     sync.Mutex
	Events []charger.Charger
}

func (p *Publisher) ChargerCreated(_ context.Context, c charger.Charger) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Events = append(p.Events, c)
	slog.Info("event (in-memory, not sent to AWS)", "type", "ChargerCreated", "id", c.ID)
	return nil
}
