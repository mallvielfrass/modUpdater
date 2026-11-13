package httpapi

import (
	"errors"
	"sync"
	"time"

	"modupdater/internal/auth"
)

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *limiter) allow(host string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(host, time.Now())
	return len(l.hits[host]) < 10
}

func (l *limiter) fail(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.prune(host, now)
	l.hits[host] = append(l.hits[host], now)
}

func (l *limiter) success(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, host)
}

func (l *limiter) prune(host string, now time.Time) {
	cut := now.Add(-10 * time.Minute)
	old := l.hits[host]
	kept := old[:0]
	for _, t := range old {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, host)
		return
	}
	l.hits[host] = kept
}

func countsFailure(err error) bool {
	return errors.Is(err, auth.ErrUnauthorized) ||
		errors.Is(err, auth.ErrInviteInvalid) ||
		errors.Is(err, auth.ErrUsernameTaken)
}
