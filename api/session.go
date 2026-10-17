package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
)

// Session limits (specs/016-ui-mvp plan.md decision 4).
const (
	DefaultSessionIdle     = 30 * time.Minute
	DefaultSessionAbsolute = 12 * time.Hour
	DefaultMaxSessions     = 1024

	// lastSeenGranularity bounds how often a request rewrites a session's
	// last-seen time, so busy polling doesn't contend on the table lock.
	lastSeenGranularity = time.Minute
	sweepEvery          = time.Minute
)

// SessionConfig configures Sessions. Zero values take the defaults above.
type SessionConfig struct {
	Idle     time.Duration
	Absolute time.Duration
	Max      int
	// Now is the clock; defaults to time.Now. For tests.
	Now func() time.Time
}

// Sessions is the in-memory table of browser login sessions (ADR 0013).
// Only the SHA-256 of a session ID is kept, so a memory dump or a log line
// can't be replayed as a cookie. Tokens never leave this table. Safe for
// concurrent use.
type Sessions struct {
	cfg SessionConfig

	mu        sync.Mutex
	byHash    map[[32]byte]*session
	lastSweep time.Time
}

// session is one browser login. Its tokens and principal are guarded by
// lock, a one-slot channel rather than a mutex so a request waiting for
// another's refresh can give up when its own context ends.
type session struct {
	hash    [32]byte
	created time.Time

	lock      chan struct{}
	principal identity.Principal
	tokens    identity.Tokens

	// lastSeen is guarded by Sessions.mu, not lock.
	lastSeen time.Time
}

// NewSessions returns an empty table.
func NewSessions(cfg SessionConfig) *Sessions {
	if cfg.Idle <= 0 {
		cfg.Idle = DefaultSessionIdle
	}
	if cfg.Absolute <= 0 {
		cfg.Absolute = DefaultSessionAbsolute
	}
	if cfg.Max <= 0 {
		cfg.Max = DefaultMaxSessions
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Sessions{cfg: cfg, byHash: map[[32]byte]*session{}}
}

// newSecret returns 32 random bytes, base64url-encoded.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashID(id string) [32]byte { return sha256.Sum256([]byte(id)) }

// create stores a new session for p and returns its ID -- the only time
// the plain ID exists server side. A fresh ID per login is what makes
// session fixation impossible.
func (s *Sessions) create(p identity.Principal, t identity.Tokens) (string, error) {
	id, err := newSecret()
	if err != nil {
		return "", err
	}
	now := s.cfg.Now()
	sess := &session{hash: hashID(id), created: now, lastSeen: now, lock: make(chan struct{}, 1), principal: p, tokens: t}

	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastSweep) >= sweepEvery {
		s.sweepLocked(now)
	}
	for len(s.byHash) >= s.cfg.Max {
		s.evictOldestLocked()
	}
	s.byHash[sess.hash] = sess
	return id, nil
}

// lookup returns the live session for id, or nil. An expired session is
// removed on the way.
func (s *Sessions) lookup(id string) *session {
	if id == "" {
		return nil
	}
	h := hashID(id)
	now := s.cfg.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byHash[h]
	if !ok {
		return nil
	}
	if s.expiredLocked(sess, now) {
		delete(s.byHash, h)
		return nil
	}
	if now.Sub(sess.lastSeen) >= lastSeenGranularity {
		sess.lastSeen = now
	}
	return sess
}

// remove deletes the session for id and returns it (nil if there was none).
func (s *Sessions) remove(id string) *session {
	h := hashID(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.byHash[h]
	delete(s.byHash, h)
	return sess
}

func (s *Sessions) removeSession(sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byHash[sess.hash] == sess {
		delete(s.byHash, sess.hash)
	}
}

// expiries returns when sess ends at the latest, and when it ends if idle.
func (s *Sessions) expiries(sess *session) (absolute, idle time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sess.created.Add(s.cfg.Absolute), sess.lastSeen.Add(s.cfg.Idle)
}

// Len reports how many sessions are held (expired ones included until the
// next sweep). For tests and metrics.
func (s *Sessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byHash)
}

func (s *Sessions) expiredLocked(sess *session, now time.Time) bool {
	return !now.Before(sess.created.Add(s.cfg.Absolute)) || !now.Before(sess.lastSeen.Add(s.cfg.Idle))
}

func (s *Sessions) sweepLocked(now time.Time) {
	s.lastSweep = now
	for h, sess := range s.byHash {
		if s.expiredLocked(sess, now) {
			delete(s.byHash, h)
		}
	}
}

// evictOldestLocked drops the least recently used session. A linear scan:
// the table holds at most a few thousand entries and this runs only when
// it is full.
func (s *Sessions) evictOldestLocked() {
	var oldest *session
	for _, sess := range s.byHash {
		if oldest == nil || sess.lastSeen.Before(oldest.lastSeen) {
			oldest = sess
		}
	}
	if oldest != nil {
		delete(s.byHash, oldest.hash)
	}
}

// acquire takes sess's lock, or gives up when ctx ends.
func (sess *session) acquire(ctx context.Context) error {
	select {
	case sess.lock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sess *session) release() { <-sess.lock }

// errSessionEnded means the provider refused to renew the session: the
// person must log in again.
var errSessionEnded = errors.New("session ended")
