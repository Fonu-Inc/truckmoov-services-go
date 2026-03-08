package redis

import "sync"

type Store struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewStore() *Store {
	return &Store{data: make(map[string][]byte)}
}

func (s *Store) Set(key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return value, ok
}
