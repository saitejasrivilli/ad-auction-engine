// Package breaker implements a Hystrix-style circuit breaker for the auction
// service. When the error rate exceeds a threshold, the breaker opens and
// requests fall back to cached scores instead of hitting downstream services.
//
// States:
//   Closed   → normal operation, errors counted
//   Open     → fast-fail, return cached fallback
//   HalfOpen → probe: allow one request, reset if successful
package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker open")

type state int

const (
	stateClosed   state = iota
	stateOpen
	stateHalfOpen
)

// Config holds tuning parameters for the circuit breaker.
type Config struct {
	// MinRequests before the error rate is evaluated.
	MinRequests int
	// ErrorThreshold as a fraction [0,1]. Default 0.5.
	ErrorThreshold float64
	// OpenDuration before transitioning to HalfOpen.
	OpenDuration time.Duration
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig() Config {
	return Config{
		MinRequests:    20,
		ErrorThreshold: 0.5,
		OpenDuration:   10 * time.Second,
	}
}

// Breaker is a thread-safe circuit breaker.
type Breaker struct {
	cfg        Config
	mu         sync.Mutex
	state      state
	total      int
	failures   int
	openedAt   time.Time

	// CachedFallback is called when the circuit is open.
	// It should return a best-effort cached result.
	CachedFallback func() (interface{}, error)
}

// New creates a Breaker with the given config.
func New(cfg Config) *Breaker {
	return &Breaker{cfg: cfg}
}

// Execute runs fn if the circuit is closed or half-open.
// On open circuit, CachedFallback is invoked instead.
func (b *Breaker) Execute(fn func() (interface{}, error)) (interface{}, error) {
	b.mu.Lock()

	switch b.state {
	case stateOpen:
		if time.Since(b.openedAt) > b.cfg.OpenDuration {
			b.state = stateHalfOpen
			fmt.Println("[breaker] half-open — probing")
		} else {
			b.mu.Unlock()
			if b.CachedFallback != nil {
				return b.CachedFallback()
			}
			return nil, ErrCircuitOpen
		}
	}

	b.mu.Unlock()

	result, err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()

	b.total++
	if err != nil {
		b.failures++
	}

	switch b.state {
	case stateHalfOpen:
		if err == nil {
			b.reset()
			fmt.Println("[breaker] closed — probe succeeded")
		} else {
			b.trip()
			fmt.Println("[breaker] open — probe failed")
		}

	case stateClosed:
		if b.total >= b.cfg.MinRequests {
			rate := float64(b.failures) / float64(b.total)
			if rate >= b.cfg.ErrorThreshold {
				b.trip()
				fmt.Printf("[breaker] open — error rate %.2f\n", rate)
			}
		}
	}

	return result, err
}

// State returns a human-readable state label.
func (b *Breaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case stateClosed:
		return "closed"
	case stateOpen:
		return "open"
	case stateHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// Stats returns (total, failures) counters.
func (b *Breaker) Stats() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total, b.failures
}

func (b *Breaker) trip() {
	b.state = stateOpen
	b.openedAt = time.Now()
}

func (b *Breaker) reset() {
	b.state = stateClosed
	b.total = 0
	b.failures = 0
}
