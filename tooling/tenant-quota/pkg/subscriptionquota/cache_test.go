// Copyright 2025 Microsoft Corporation
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
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func newTestMetric(name string) prometheus.Metric {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Name: fmt.Sprintf("test_metric_%s", name),
		Help: "test metric",
	})
}

func TestMetricsCacheSetAndGetAll(t *testing.T) {
	type testCase struct {
		name             string
		seedExpiredEntry bool
		wantVisible      int
		wantLen          int
	}

	testCases := []testCase{
		{
			name:             "set stores a visible metric",
			seedExpiredEntry: false,
			wantVisible:      1,
			wantLen:          1,
		},
		{
			name:             "expired entries are hidden but still counted in len",
			seedExpiredEntry: true,
			wantVisible:      1,
			wantLen:          2,
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			cache := NewMetricsCache(30 * time.Second)
			cache.Set("fresh", newTestMetric("fresh"))

			if tc.seedExpiredEntry {
				cache.entries["expired"] = cacheEntry{
					metric:  newTestMetric("expired"),
					created: time.Now().Add(-time.Hour),
				}
			}

			if got := len(cache.GetAll()); got != tc.wantVisible {
				t.Fatalf("len(GetAll()) = %d, want %d", got, tc.wantVisible)
			}
			if got := cache.Len(); got != tc.wantLen {
				t.Fatalf("Len() = %d, want %d", got, tc.wantLen)
			}
		})
	}
}

func TestMetricsCachePrune(t *testing.T) {
	type testCase struct {
		name          string
		entries       map[string]time.Duration
		wantRemaining []string
	}

	testCases := []testCase{
		{
			name: "prunes expired entries",
			entries: map[string]time.Duration{
				"fresh":   time.Second,
				"expired": time.Hour,
			},
			wantRemaining: []string{"fresh"},
		},
		{
			name: "keeps all fresh entries",
			entries: map[string]time.Duration{
				"fresh-a": time.Second,
				"fresh-b": 2 * time.Second,
			},
			wantRemaining: []string{"fresh-a", "fresh-b"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewMetricsCache(30 * time.Second)
			for key, age := range tc.entries {
				cache.entries[key] = cacheEntry{
					metric:  newTestMetric(key),
					created: time.Now().Add(-age),
				}
			}

			cache.Prune()

			if got := cache.Len(); got != len(tc.wantRemaining) {
				t.Fatalf("Len() after prune = %d, want %d", got, len(tc.wantRemaining))
			}
			for _, key := range tc.wantRemaining {
				if _, ok := cache.entries[key]; !ok {
					t.Fatalf("expected cache to contain %q after prune, got %#v", key, cache.entries)
				}
			}
		})
	}
}

func TestCacheKey(t *testing.T) {
	type testCase struct {
		name           string
		source         string
		subscriptionID string
		region         string
		quotaName      string
		metricType     string
		want           string
	}

	testCases := []testCase{
		{
			name:           "regional usage metric",
			source:         "compute",
			subscriptionID: "sub-1",
			region:         "eastus",
			quotaName:      "cores",
			metricType:     "usage",
			want:           "compute/sub-1/eastus/cores/usage",
		},
		{
			name:           "non regional limit metric",
			source:         "rbac",
			subscriptionID: "sub-2",
			region:         "",
			quotaName:      "roleAssignments",
			metricType:     "limit",
			want:           "rbac/sub-2//roleAssignments/limit",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			if got := cacheKey(tc.source, tc.subscriptionID, tc.region, tc.quotaName, tc.metricType); got != tc.want {
				t.Fatalf("cacheKey() = %q, want %q", got, tc.want)
			}
		})
	}
}
