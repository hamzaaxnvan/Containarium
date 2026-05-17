package notify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a user has no channel.
var ErrNotFound = errors.New("notify: no channel configured")

// ChannelStore holds one ntfy channel per user.
type ChannelStore interface {
	Get(ctx context.Context, username string) (Channel, error)
	Set(ctx context.Context, ch Channel) (Channel, error)
	Delete(ctx context.Context, username string) error
	List(ctx context.Context) ([]Channel, error)
}

// MemStore is an in-memory ChannelStore, for tests and for a daemon without
// Postgres.
type MemStore struct {
	mu sync.Mutex
	m  map[string]Channel
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{m: map[string]Channel{}} }

func (s *MemStore) Get(_ context.Context, username string) (Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.m[username]
	if !ok {
		return Channel{}, ErrNotFound
	}
	return ch, nil
}

func (s *MemStore) Set(_ context.Context, ch Channel) (Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch.UpdatedAt = time.Now().UTC()
	s.m[ch.Username] = ch
	return ch, nil
}

func (s *MemStore) Delete(_ context.Context, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
