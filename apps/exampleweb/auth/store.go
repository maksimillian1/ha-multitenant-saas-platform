package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/core"
)

// Store keeps sessions in the memory of one pod: a restart or another replica
// signs the user out, which a prototype accepts.
type Store struct {
	mu       sync.Mutex
	sessions map[string]core.Session
	now      func() time.Time
}

func NewStore() *Store {
	return &Store{sessions: map[string]core.Session{}, now: time.Now}
}

func (s *Store) Create(session core.Session) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = session
	return id
}

func (s *Store) Get(id string) (core.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return core.Session{}, false
	}
	if !s.now().Before(session.Expires) {
		delete(s.sessions, id)
		return core.Session{}, false
	}
	return session, true
}

// Update replaces a session that still exists; a session deleted by logout stays deleted.
func (s *Store) Update(id string, session core.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; ok {
		s.sessions[id] = session
	}
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}
