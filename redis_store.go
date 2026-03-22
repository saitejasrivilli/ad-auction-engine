// Package pacing provides Redis-backed and in-memory budget stores.
package pacing

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/redis/go-redis/v9"
)

// ── Redis store ───────────────────────────────────────────────────────────────

const bucketKeyPrefix = "budget:tokens:"

// RedisStore implements BudgetStore using Redis INCRBYFLOAT for atomic ops.
// Tokens are stored as floats (USD) with a cap equal to one second's worth.
type RedisStore struct {
	client  *redis.Client
	maxCaps map[string]float64 // per-advertiser max bucket size (1 sec)
	mu      sync.RWMutex
}

// NewRedisStore creates a RedisStore connected to the given Redis address.
func NewRedisStore(addr string) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}
	return &RedisStore{
		client:  client,
		maxCaps: make(map[string]float64),
	}, nil
}

// SetMaxCap sets the bucket cap for an advertiser (called during registration).
func (r *RedisStore) SetMaxCap(advID string, cap float64) {
	r.mu.Lock()
	r.maxCaps[advID] = cap
	r.mu.Unlock()
}

func (r *RedisStore) key(advID string) string {
	return bucketKeyPrefix + advID
}

// Deduct atomically subtracts amount from the bucket using a Lua script to
// prevent the balance from going negative.
func (r *RedisStore) Deduct(ctx context.Context, advID string, amount float64) (float64, bool, error) {
	// Lua: atomic read-and-decrement with floor at 0
	script := redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1])) or 0
if cur < tonumber(ARGV[1]) then
  return tostring(cur)
end
local new = cur - tonumber(ARGV[1])
redis.call('SET', KEYS[1], tostring(new))
return tostring(new)
`)
	res, err := script.Run(ctx, r.client, []string{r.key(advID)}, amount).Text()
	if err != nil {
		return 0, false, fmt.Errorf("deduct lua: %w", err)
	}

	var remaining float64
	fmt.Sscanf(res, "%f", &remaining)

	r.mu.RLock()
	cap := r.maxCaps[advID]
	r.mu.RUnlock()

	throttled := cap > 0 && remaining < amount
	return remaining, throttled, nil
}

// Refill adds tokens up to the bucket cap.
func (r *RedisStore) Refill(ctx context.Context, advID string, tokens float64) error {
	r.mu.RLock()
	cap := r.maxCaps[advID]
	r.mu.RUnlock()

	script := redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1])) or 0
local new = math.min(cur + tonumber(ARGV[1]), tonumber(ARGV[2]))
redis.call('SET', KEYS[1], tostring(new))
return tostring(new)
`)
	return script.Run(ctx, r.client, []string{r.key(advID)}, tokens, cap).Err()
}

// Remaining returns current token balance.
func (r *RedisStore) Remaining(ctx context.Context, advID string) (float64, error) {
	val, err := r.client.Get(ctx, r.key(advID)).Float64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

// ── In-memory store (testing / local dev) ────────────────────────────────────

// InMemoryStore is a thread-safe in-process BudgetStore.
// Suitable for unit tests and local benchmarks without Redis.
type InMemoryStore struct {
	mu      sync.Mutex
	buckets map[string]float64
	caps    map[string]float64
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		buckets: make(map[string]float64),
		caps:    make(map[string]float64),
	}
}

func (m *InMemoryStore) SetMaxCap(advID string, cap float64) {
	m.mu.Lock()
	m.caps[advID] = cap
	m.mu.Unlock()
}

func (m *InMemoryStore) Deduct(ctx context.Context, advID string, amount float64) (float64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.buckets[advID]
	if cur < amount {
		return cur, true, nil
	}
	m.buckets[advID] = cur - amount
	throttled := m.buckets[advID] < amount
	return m.buckets[advID], throttled, nil
}

func (m *InMemoryStore) Refill(ctx context.Context, advID string, tokens float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cap := m.caps[advID]
	if cap == 0 {
		cap = math.MaxFloat64
	}
	cur := m.buckets[advID]
	m.buckets[advID] = math.Min(cur+tokens, cap)
	return nil
}

func (m *InMemoryStore) Remaining(ctx context.Context, advID string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buckets[advID], nil
}
