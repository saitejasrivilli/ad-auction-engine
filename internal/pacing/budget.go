// Package pacing implements smooth budget pacing via a token-bucket algorithm.
// Each advertiser has a daily budget split into per-second token refills.
// Spend requests atomically decrement the bucket; when empty, the advertiser
// is throttled until the next refill tick.
//
// Design rationale:
//   - Token bucket prevents burst spend (common with simple daily cap checks)
//   - Redis atomic DECR ensures consistency across multiple auction pods
//   - Smooth pacing avoids the "end-of-day cliff" where budget exhausts at 11pm
package pacing

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	defaultDaySeconds  = 86400
	refillInterval     = time.Second
	minBucketCapTokens = 1
)

// BudgetPacer manages per-advertiser token buckets.
// In production, token state lives in Redis (see RedisStore).
// The InMemoryStore is provided for testing and local benchmarks.
type BudgetPacer struct {
	store    BudgetStore
	mu       sync.RWMutex
	budgets  map[string]float64 // daily budget per advertiser
	stopCh   chan struct{}
}

// BudgetStore abstracts the token state backend.
type BudgetStore interface {
	// Deduct attempts to spend `amount` tokens for an advertiser.
	// Returns (remaining, throttled, error).
	Deduct(ctx context.Context, advertiserID string, amount float64) (float64, bool, error)

	// Refill adds tokens to the bucket (called every refillInterval).
	Refill(ctx context.Context, advertiserID string, tokens float64) error

	// Remaining returns the current token count.
	Remaining(ctx context.Context, advertiserID string) (float64, error)
}

// NewBudgetPacer creates a pacer backed by the given store and starts the
// background refill goroutine.
func NewBudgetPacer(store BudgetStore) *BudgetPacer {
	bp := &BudgetPacer{
		store:   store,
		budgets: make(map[string]float64),
		stopCh:  make(chan struct{}),
	}
	go bp.refillLoop()
	return bp
}

// RegisterAdvertiser sets the daily budget for an advertiser.
// The initial bucket is pre-filled with one second's worth of tokens.
func (bp *BudgetPacer) RegisterAdvertiser(ctx context.Context, advID string, dailyBudget float64) error {
	bp.mu.Lock()
	bp.budgets[advID] = dailyBudget
	bp.mu.Unlock()

	tokensPerSec := bp.tokensPerSecond(dailyBudget)
	return bp.store.Refill(ctx, advID, tokensPerSec)
}

// TrySpend attempts to deduct clearingPriceCPM / 1000 from the advertiser's
// token bucket (converting CPM to cost-per-impression).
// Returns throttled=true when budget is exhausted.
func (bp *BudgetPacer) TrySpend(ctx context.Context, advID string, clearingPriceCPM float64) (throttled bool, err error) {
	cost := clearingPriceCPM / 1000.0 // CPM → per-impression cost
	_, throttled, err = bp.store.Deduct(ctx, advID, cost)
	return
}

// SpendRate returns the configured per-second token refill rate.
func (bp *BudgetPacer) SpendRate(advID string) float64 {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	return bp.tokensPerSecond(bp.budgets[advID])
}

// Remaining returns the current token balance for an advertiser.
func (bp *BudgetPacer) Remaining(ctx context.Context, advID string) (float64, error) {
	return bp.store.Remaining(ctx, advID)
}

// Stop shuts down the background refill goroutine.
func (bp *BudgetPacer) Stop() { close(bp.stopCh) }

func (bp *BudgetPacer) tokensPerSecond(dailyBudget float64) float64 {
	tps := dailyBudget / defaultDaySeconds
	return math.Max(tps, float64(minBucketCapTokens)/1000.0)
}

func (bp *BudgetPacer) refillLoop() {
	ticker := time.NewTicker(refillInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bp.stopCh:
			return
		case <-ticker.C:
			bp.mu.RLock()
			budgets := make(map[string]float64, len(bp.budgets))
			for k, v := range bp.budgets {
				budgets[k] = v
			}
			bp.mu.RUnlock()

			ctx := context.Background()
			for advID, daily := range budgets {
				tokens := bp.tokensPerSecond(daily)
				if err := bp.store.Refill(ctx, advID, tokens); err != nil {
					fmt.Printf("[pacer] refill error for %s: %v\n", advID, err)
				}
			}
		}
	}
}
