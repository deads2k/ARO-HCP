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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// minimalValidTenant returns a TenantConfig with all required fields set.
func minimalValidTenant() TenantConfig {
	return TenantConfig{
		TenantID:                 "tenant-id-1",
		ServicePrincipalClientId: "sp-client-id",
		KeyVaultSecretName:       "kv-secret",
	}
}

// minimalValidConfig returns a Config with all required fields and no optional ones.
func minimalValidConfig() Config {
	return Config{
		Tenants: []TenantConfig{minimalValidTenant()},
	}
}

// writeYAML writes content to a temp file and returns the path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// boolPtr is a helper to get a pointer to a bool literal.
func boolPtr(b bool) *bool { return &b }

// ---- Validate ---------------------------------------------------------------

func TestValidate_Defaults(t *testing.T) {
	cfg := minimalValidConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GetInterval() != DefaultInterval {
		t.Errorf("interval: got %v, want %v", cfg.GetInterval(), DefaultInterval)
	}
	if cfg.GetTimeout() != DefaultTimeout {
		t.Errorf("timeout: got %v, want %v", cfg.GetTimeout(), DefaultTimeout)
	}
	if cfg.GetCacheTTL() != DefaultCacheTTL {
		t.Errorf("cacheTTL: got %v, want %v", cfg.GetCacheTTL(), DefaultCacheTTL)
	}
}

func TestValidate_ExplicitDurations(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Interval = "5m"
	cfg.Timeout = "10s"
	cfg.CacheTTL = "1h"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GetInterval() != 5*time.Minute {
		t.Errorf("interval: got %v, want 5m", cfg.GetInterval())
	}
	if cfg.GetTimeout() != 10*time.Second {
		t.Errorf("timeout: got %v, want 10s", cfg.GetTimeout())
	}
	if cfg.GetCacheTTL() != time.Hour {
		t.Errorf("cacheTTL: got %v, want 1h", cfg.GetCacheTTL())
	}
}

func TestValidate_InvalidDurations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "bad interval",
			mutate:  func(c *Config) { c.Interval = "notaduration" },
			wantErr: "invalid interval",
		},
		{
			name:    "zero interval",
			mutate:  func(c *Config) { c.Interval = "0s" },
			wantErr: "interval must be positive",
		},
		{
			name:    "negative interval",
			mutate:  func(c *Config) { c.Interval = "-1m" },
			wantErr: "interval must be positive",
		},
		{
			name:    "bad timeout",
			mutate:  func(c *Config) { c.Timeout = "xyz" },
			wantErr: "invalid timeout",
		},
		{
			name:    "bad cacheTTL",
			mutate:  func(c *Config) { c.CacheTTL = "bad" },
			wantErr: "invalid cacheTTL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			got := err.Error()
			if len(got) == 0 {
				t.Fatal("error message is empty")
			}
			if tc.wantErr != "" && !strings.Contains(got, tc.wantErr) {
				t.Errorf("error %q does not contain %q", got, tc.wantErr)
			}
		})
	}
}

func TestValidate_NoTenants(t *testing.T) {
	cfg := Config{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty tenants")
	}
}

func TestValidate_TenantMissingFields(t *testing.T) {
	cases := []struct {
		name   string
		tenant TenantConfig
	}{
		{
			name:   "missing tenantId",
			tenant: TenantConfig{ServicePrincipalClientId: "sp", KeyVaultSecretName: "kv"},
		},
		{
			name:   "missing servicePrincipalClientId",
			tenant: TenantConfig{TenantID: "tid", KeyVaultSecretName: "kv"},
		},
		{
			name:   "missing keyVaultSecretName",
			tenant: TenantConfig{TenantID: "tid", ServicePrincipalClientId: "sp"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Tenants: []TenantConfig{tc.tenant}}
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestValidate_DuplicateTenantID(t *testing.T) {
	cfg := Config{
		Tenants: []TenantConfig{
			minimalValidTenant(),
			minimalValidTenant(), // same TenantID
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for duplicate tenantId")
	}
}

func TestValidate_SubscriptionMissingFields(t *testing.T) {
	cases := []struct {
		name string
		sub  SubscriptionConfig
	}{
		{
			name: "missing subscriptionId",
			sub:  SubscriptionConfig{Name: "sub", Regions: []string{"eastus"}},
		},
		{
			name: "missing name",
			sub:  SubscriptionConfig{SubscriptionID: "sid", Regions: []string{"eastus"}},
		},
		{
			name: "missing regions",
			sub:  SubscriptionConfig{SubscriptionID: "sid", Name: "sub"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := minimalValidTenant()
			tenant.Subscriptions = []SubscriptionConfig{tc.sub}
			cfg := Config{Tenants: []TenantConfig{tenant}}
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestValidate_ValidSubscriptions(t *testing.T) {
	tenant := minimalValidTenant()
	tenant.Subscriptions = []SubscriptionConfig{
		{SubscriptionID: "sub-1", Name: "prod", Regions: []string{"eastus", "westus"}},
	}
	cfg := Config{Tenants: []TenantConfig{tenant}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- LoadFromFile -----------------------------------------------------------

func TestLoadFromFile_Valid(t *testing.T) {
	yaml := `
tenants:
  - tenantId: "tid-1"
    servicePrincipalClientId: "sp-id"
    keyVaultSecretName: "kv-secret"
    subscriptions:
      - subscriptionId: "sub-1"
        name: "prod"
        regions:
          - eastus
`
	path := writeYAML(t, yaml)
	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Tenants) != 1 {
		t.Fatalf("tenants: got %d, want 1", len(cfg.Tenants))
	}
	if cfg.Tenants[0].TenantID != "tid-1" {
		t.Errorf("tenantId: got %q, want %q", cfg.Tenants[0].TenantID, "tid-1")
	}
}

func TestLoadFromFile_NotFound(t *testing.T) {
	_, err := LoadFromFile(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadFromFile_InvalidYAML(t *testing.T) {
	path := writeYAML(t, "{ this is: [not valid yaml")
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadFromFile_FailsValidation(t *testing.T) {
	path := writeYAML(t, "tenants: []\n")
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected validation error for empty tenants")
	}
}

// ---- HasSubscriptions -------------------------------------------------------

func TestHasSubscriptions(t *testing.T) {
	withSubs := minimalValidTenant()
	withSubs.Subscriptions = []SubscriptionConfig{
		{SubscriptionID: "sid", Name: "s", Regions: []string{"eastus"}},
	}

	cases := []struct {
		name    string
		tenants []TenantConfig
		want    bool
	}{
		{"no tenants", nil, false},
		{"tenant without subs", []TenantConfig{minimalValidTenant()}, false},
		{"tenant with subs", []TenantConfig{withSubs}, true},
		{"mixed", []TenantConfig{minimalValidTenant(), withSubs}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Tenants: tc.tenants}
			if got := cfg.HasSubscriptions(); got != tc.want {
				t.Errorf("HasSubscriptions() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---- TenantConfig helpers ---------------------------------------------------

func TestTenantConfig_GetScope(t *testing.T) {
	t.Run("custom scope", func(t *testing.T) {
		tc := TenantConfig{Scope: "https://custom.example.com/.default"}
		if got := tc.GetScope(); got != tc.Scope {
			t.Errorf("got %q, want %q", got, tc.Scope)
		}
	})
	t.Run("default scope", func(t *testing.T) {
		tc := TenantConfig{}
		if got := tc.GetScope(); got != DefaultScope {
			t.Errorf("got %q, want %q", got, DefaultScope)
		}
	})
}

func TestTenantConfig_GetDisplayName(t *testing.T) {
	t.Run("with name", func(t *testing.T) {
		tc := TenantConfig{TenantID: "tid", TenantName: "My Tenant"}
		if got := tc.GetDisplayName(); got != "My Tenant" {
			t.Errorf("got %q, want %q", got, "My Tenant")
		}
	})
	t.Run("falls back to id", func(t *testing.T) {
		tc := TenantConfig{TenantID: "tid"}
		if got := tc.GetDisplayName(); got != "tid" {
			t.Errorf("got %q, want %q", got, "tid")
		}
	})
}

func TestTenantConfig_IsDirectoryQuotaEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		tc := TenantConfig{}
		if !tc.IsDirectoryQuotaEnabled() {
			t.Error("expected true when DirectoryQuota is nil")
		}
	})
	t.Run("explicit true", func(t *testing.T) {
		tc := TenantConfig{DirectoryQuota: boolPtr(true)}
		if !tc.IsDirectoryQuotaEnabled() {
			t.Error("expected true")
		}
	})
	t.Run("explicit false", func(t *testing.T) {
		tc := TenantConfig{DirectoryQuota: boolPtr(false)}
		if tc.IsDirectoryQuotaEnabled() {
			t.Error("expected false")
		}
	})
}

// ---- SubscriptionConfig helpers ---------------------------------------------

func TestSubscriptionConfig_GetRoleAssignmentLimit(t *testing.T) {
	t.Run("custom limit", func(t *testing.T) {
		s := SubscriptionConfig{RoleAssignmentLimit: 1000}
		if got := s.GetRoleAssignmentLimit(); got != 1000 {
			t.Errorf("got %d, want 1000", got)
		}
	})
	t.Run("zero falls back to default", func(t *testing.T) {
		s := SubscriptionConfig{}
		if got := s.GetRoleAssignmentLimit(); got != DefaultRoleAssignmentLimit {
			t.Errorf("got %d, want %d", got, DefaultRoleAssignmentLimit)
		}
	})
	t.Run("negative falls back to default", func(t *testing.T) {
		s := SubscriptionConfig{RoleAssignmentLimit: -1}
		if got := s.GetRoleAssignmentLimit(); got != DefaultRoleAssignmentLimit {
			t.Errorf("got %d, want %d", got, DefaultRoleAssignmentLimit)
		}
	})
}
