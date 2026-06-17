package audit

import "sync"

type Store struct {
	mu      sync.Mutex
	entries []string
}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Append(entry string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

func (s *Store) Entries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	copy(out, s.entries)
	return out
}
