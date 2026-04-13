// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package subscriptionquota

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type cacheEntry struct {
	metric  prometheus.Metric
	created time.Time
}

// MetricsCache is a thread-safe cache for Prometheus metrics with TTL-based
// expiry. It is designed for auto-discovered quota metrics where the set of
// active label combinations changes over time (e.g. VM families that appear
// and disappear depending on which CI tests are running).
type MetricsCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

func NewMetricsCache(ttl time.Duration) *MetricsCache {
	return &MetricsCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

// Set adds or updates a metric in the cache, resetting its TTL.
func (c *MetricsCache) Set(key string, metric prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{metric: metric, created: time.Now()}
}

// GetAll returns all non-expired metrics from the cache.
func (c *MetricsCache) GetAll() []prometheus.Metric {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	result := make([]prometheus.Metric, 0, len(c.entries))
	for _, e := range c.entries {
		if now.Sub(e.created) < c.ttl {
			result = append(result, e.metric)
		}
	}
	return result
}

// Prune removes all expired entries from the cache.
func (c *MetricsCache) Prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.entries {
		if now.Sub(e.created) >= c.ttl {
			delete(c.entries, k)
		}
	}
}

// Len returns the number of entries currently in the cache (including expired).
func (c *MetricsCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
