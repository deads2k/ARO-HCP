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

package tenantquota

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errorReadCloser struct {
	err error
}

func (e errorReadCloser) Read(_ []byte) (int, error) {
	return 0, e.err
}

func (e errorReadCloser) Close() error {
	return nil
}

func TestParseOrganizationResponse(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		body       string
		tenant     config.TenantConfig
		assertions func(t *testing.T, got *QuotaData, err error)
	}

	testCases := []testCase{
		{
			name:   "invalid json",
			body:   `{"value": [`,
			tenant: config.TenantConfig{TenantID: "tenant-id"},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "decode response") {
					t.Fatalf("expected decode error, got %v", err)
				}
			},
		},
		{
			name:   "empty organization list",
			body:   `{"value":[]}`,
			tenant: config.TenantConfig{TenantID: "tenant-id"},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "no organization data in response") {
					t.Fatalf("expected empty organization error, got %v", err)
				}
			},
		},
		{
			name: "missing used quota",
			body: `{"value":[{"displayName":"Graph Tenant","directorySizeQuota":{"total":8}}]}`,
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "incomplete quota data") {
					t.Fatalf("expected incomplete quota error, got %v", err)
				}
			},
		},
		{
			name: "invalid total",
			body: `{"value":[{"displayName":"Graph Tenant","directorySizeQuota":{"used":1,"total":0}}]}`,
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "invalid quota total: 0") {
					t.Fatalf("expected invalid total error, got %v", err)
				}
			},
		},
		{
			name: "falls back to display name and calculates values",
			body: `{"value":[{"displayName":"Graph Tenant","directorySizeQuota":{"used":3,"total":8}}]}`,
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			assertions: func(t *testing.T, got *QuotaData, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil {
					t.Fatal("expected quota data, got nil")
					return
				}
				if got.TenantID != "tenant-id" {
					t.Fatalf("TenantID = %q, want %q", got.TenantID, "tenant-id")
				}
				if got.TenantName != "Graph Tenant" {
					t.Fatalf("TenantName = %q, want %q", got.TenantName, "Graph Tenant")
				}
				if got.UsagePercentage != 37 {
					t.Fatalf("UsagePercentage = %d, want %d", got.UsagePercentage, 37)
				}
				if got.QuotaTotal != 8 {
					t.Fatalf("QuotaTotal = %d, want %d", got.QuotaTotal, 8)
				}
				if got.QuotaUsed != 3 {
					t.Fatalf("QuotaUsed = %d, want %d", got.QuotaUsed, 3)
				}
				if got.RemainingCapacity != 5 {
					t.Fatalf("RemainingCapacity = %d, want %d", got.RemainingCapacity, 5)
				}
				if got.Timestamp.IsZero() {
					t.Fatal("expected non-zero timestamp")
				}
			},
		},
		{
			name: "uses configured tenant name override",
			body: `{"value":[{"displayName":"Graph Tenant","directorySizeQuota":{"used":12,"total":24}}]}`,
			tenant: config.TenantConfig{
				TenantID:   "tenant-id",
				TenantName: "Configured Tenant",
			},
			assertions: func(t *testing.T, got *QuotaData, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil {
					t.Fatal("expected quota data, got nil")
					return
				}
				if got.TenantName != "Configured Tenant" {
					t.Fatalf("TenantName = %q, want %q", got.TenantName, "Configured Tenant")
				}
				if got.UsagePercentage != 50 {
					t.Fatalf("UsagePercentage = %d, want %d", got.UsagePercentage, 50)
				}
				if got.RemainingCapacity != 12 {
					t.Fatalf("RemainingCapacity = %d, want %d", got.RemainingCapacity, 12)
				}
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseOrganizationResponse(strings.NewReader(tc.body), tc.tenant)
			tc.assertions(t, got, err)
		})
	}
}

func TestFetchQuotaFromAPI(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		token      string
		tenant     config.TenantConfig
		transport  roundTripFunc
		assertions func(t *testing.T, got *QuotaData, err error)
	}

	testCases := []testCase{
		{
			name:  "success parses response and sets request headers",
			token: "test-token",
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			transport: func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					t.Fatalf("Method = %q, want %q", req.Method, http.MethodGet)
				}
				if req.URL.String() != graphAPIEndpoint {
					t.Fatalf("URL = %q, want %q", req.URL.String(), graphAPIEndpoint)
				}
				if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Fatalf("Authorization header = %q, want %q", got, "Bearer test-token")
				}
				if got := req.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("Content-Type header = %q, want %q", got, "application/json")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"value":[{"displayName":"Graph Tenant","directorySizeQuota":{"used":6,"total":12}}]}`,
					)),
				}, nil
			},
			assertions: func(t *testing.T, got *QuotaData, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil {
					t.Fatal("expected quota data, got nil")
					return
				}
				if got.TenantName != "Graph Tenant" {
					t.Fatalf("TenantName = %q, want %q", got.TenantName, "Graph Tenant")
				}
				if got.UsagePercentage != 50 {
					t.Fatalf("UsagePercentage = %d, want %d", got.UsagePercentage, 50)
				}
			},
		},
		{
			name:  "transport error is wrapped",
			token: "test-token",
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			transport: func(_ *http.Request) (*http.Response, error) {
				return nil, errors.New("request boom")
			},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "execute request:") || !strings.Contains(err.Error(), "request boom") {
					t.Fatalf("expected wrapped transport error, got %v", err)
				}
			},
		},
		{
			name:  "non-200 response returns body",
			token: "test-token",
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			transport: func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("boom")),
				}, nil
			},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "API returned 500: boom") {
					t.Fatalf("expected body in error, got %v", err)
				}
			},
		},
		{
			name:  "non-200 response with unreadable body is wrapped",
			token: "test-token",
			tenant: config.TenantConfig{
				TenantID: "tenant-id",
			},
			transport: func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       errorReadCloser{err: errors.New("read boom")},
				}, nil
			},
			assertions: func(t *testing.T, _ *QuotaData, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "API returned 502 (failed to read body: read boom)") {
					t.Fatalf("expected body read error, got %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &QuotaClient{
				httpClient: &http.Client{Transport: tc.transport},
				logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			}

			got, err := client.fetchQuotaFromAPI(context.Background(), tc.token, tc.tenant)
			tc.assertions(t, got, err)
		})
	}
}
