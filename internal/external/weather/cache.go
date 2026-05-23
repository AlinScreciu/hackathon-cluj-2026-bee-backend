package weather

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type cacheEntry struct {
	result *Result
	expiry time.Time
}

// CachedClient wraps the Fetch function with an in-process TTL cache.
// Cache key: "lat,lng" rounded to 4 decimal places (~11m resolution).
type CachedClient struct {
	ttl   time.Duration
	cache sync.Map // key: string → *cacheEntry
}

func NewCachedClient(ttl time.Duration) *CachedClient {
	return &CachedClient{ttl: ttl}
}

// Get returns weather for the given coordinates, using cache if available.
func (c *CachedClient) Get(ctx context.Context, lat, lng float64) (*Result, error) {
	key := fmt.Sprintf("%.4f,%.4f", lat, lng)

	if v, ok := c.cache.Load(key); ok {
		entry := v.(*cacheEntry)
		if time.Now().Before(entry.expiry) {
			return entry.result, nil
		}
		c.cache.Delete(key)
	}

	result, err := Fetch(ctx, lat, lng)
	if err != nil {
		return nil, err
	}

	c.cache.Store(key, &cacheEntry{
		result: result,
		expiry: time.Now().Add(c.ttl),
	})
	return result, nil
}
