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

package credentials

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
)

func TestSecretPath(t *testing.T) {
	type testCase struct {
		name      string
		basePath  string
		secret    string
		wantPath  string
		useEnvVar bool
	}

	testCases := []testCase{
		{
			name:      "uses env override",
			basePath:  "/tmp/custom-secrets",
			secret:    "tenant-secret",
			wantPath:  "/tmp/custom-secrets/tenant-secret",
			useEnvVar: true,
		},
		{
			name:      "falls back to default path",
			secret:    "tenant-secret",
			wantPath:  secretsStorePath + "/tenant-secret",
			useEnvVar: false,
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			if tc.useEnvVar {
				t.Setenv(secretsStoreEnvVar, tc.basePath)
			} else {
				t.Setenv(secretsStoreEnvVar, "")
			}

			if got := secretPath(tc.secret); got != tc.wantPath {
				t.Fatalf("secretPath() = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestReadSecret(t *testing.T) {
	type testCase struct {
		name       string
		setup      func(t *testing.T, secretName string)
		secretName string
		assertions func(t *testing.T, got string, err error)
	}

	testCases := []testCase{
		{
			name:       "reads and trims whitespace",
			secretName: "tenant-secret",
			setup: func(t *testing.T, secretName string) {
				t.Helper()
				basePath := t.TempDir()
				t.Setenv(secretsStoreEnvVar, basePath)
				if err := os.WriteFile(filepath.Join(basePath, secretName), []byte("  super-secret \n"), 0o644); err != nil {
					t.Fatalf("write secret file: %v", err)
				}
			},
			assertions: func(t *testing.T, got string, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got != "super-secret" {
					t.Fatalf("readSecret() = %q, want %q", got, "super-secret")
				}
			},
		},
		{
			name:       "missing file returns path context",
			secretName: "missing-secret",
			setup: func(t *testing.T, _ string) {
				t.Helper()
				t.Setenv(secretsStoreEnvVar, t.TempDir())
			},
			assertions: func(t *testing.T, _ string, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "missing-secret") {
					t.Fatalf("expected missing secret path in error, got %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t, tc.secretName)
			got, err := readSecret(tc.secretName)
			tc.assertions(t, got, err)
		})
	}
}

func TestCredentialCacheKey(t *testing.T) {
	type testCase struct {
		name   string
		tenant config.TenantConfig
		want   string
	}

	testCases := []testCase{
		{
			name: "tenant and client id",
			tenant: config.TenantConfig{
				TenantID:                 "tenant-a",
				ServicePrincipalClientId: "client-a",
			},
			want: "tenant-a:client-a",
		},
		{
			name: "empty values still use separator",
			tenant: config.TenantConfig{
				TenantID:                 "",
				ServicePrincipalClientId: "",
			},
			want: ":",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			if got := credentialCacheKey(tc.tenant); got != tc.want {
				t.Fatalf("credentialCacheKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderInvalidateCredentials(t *testing.T) {
	type testCase struct {
		name          string
		initialKeys   []string
		invalidate    []string
		wantRemaining []string
	}

	testCases := []testCase{
		{
			name:          "removes matching cached credentials",
			initialKeys:   []string{"tenant-a:client-a", "tenant-b:client-b"},
			invalidate:    []string{"tenant-a:client-a"},
			wantRemaining: []string{"tenant-b:client-b"},
		},
		{
			name:          "ignores missing cache keys",
			initialKeys:   []string{"tenant-a:client-a"},
			invalidate:    []string{"tenant-missing:client-missing"},
			wantRemaining: []string{"tenant-a:client-a"},
		},
		{
			name:          "handles multiple removals",
			initialKeys:   []string{"tenant-a:client-a", "tenant-b:client-b", "tenant-c:client-c"},
			invalidate:    []string{"tenant-a:client-a", "tenant-c:client-c"},
			wantRemaining: []string{"tenant-b:client-b"},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			provider := NewProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
			provider.credCache = make(map[string]*azidentity.ClientSecretCredential, len(tc.initialKeys))
			for _, key := range tc.initialKeys {
				provider.credCache[key] = nil
			}

			provider.invalidateCredentials("tenant-secret", tc.invalidate)

			if len(provider.credCache) != len(tc.wantRemaining) {
				t.Fatalf("credential cache len = %d, want %d", len(provider.credCache), len(tc.wantRemaining))
			}
			for _, key := range tc.wantRemaining {
				if _, ok := provider.credCache[key]; !ok {
					t.Fatalf("expected cache to contain %q, got %#v", key, provider.credCache)
				}
			}
		})
	}
}

// atomicUpdateFile simulates the AtomicWriter rotation pattern used by the CSI
// driver: write into a new versioned directory and atomically swap the ..data
// symlink that the exposed secret file points to.
func atomicUpdateFile(t *testing.T, dir, filename, version, content string) {
	t.Helper()

	versionedDir := filepath.Join(dir, fmt.Sprintf("..%s", version))
	if err := os.MkdirAll(versionedDir, 0o755); err != nil {
		t.Fatalf("create versioned dir: %v", err)
	}

	secretPath := filepath.Join(versionedDir, filename)
	if err := os.WriteFile(secretPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	dataLink := filepath.Join(dir, "..data")
	dataTmpLink := filepath.Join(dir, "..data_tmp")
	_ = os.Remove(dataTmpLink)

	if err := os.Symlink(filepath.Base(versionedDir), dataTmpLink); err != nil {
		t.Fatalf("create temp symlink: %v", err)
	}
	if err := os.Rename(dataTmpLink, dataLink); err != nil {
		t.Fatalf("swap data symlink: %v", err)
	}

	secretLink := filepath.Join(dir, filename)
	if _, err := os.Lstat(secretLink); os.IsNotExist(err) {
		if err := os.Symlink(filepath.Join("..data", filename), secretLink); err != nil {
			t.Fatalf("create secret symlink: %v", err)
		}
	}
}

func waitForCondition(t *testing.T, timeout, interval time.Duration, cond func() bool, message string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatal(message)
}

func TestProviderReloadsAfterSecretRotation(t *testing.T) {
	mountDir := t.TempDir()
	secretName := "tenant-secret"
	atomicUpdateFile(t, mountDir, secretName, "v1", "initial-secret")
	t.Setenv(secretsStoreEnvVar, mountDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := NewProvider(logger)
	provider.watchInterval = 50 * time.Millisecond

	tenant := config.TenantConfig{
		TenantID:                 "tenant-id",
		ServicePrincipalClientId: "client-id",
		KeyVaultSecretName:       secretName,
	}

	initialCred, err := provider.GetCredential(tenant)
	if err != nil {
		t.Fatalf("get initial credential: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := provider.StartWatching(ctx, []config.TenantConfig{tenant}); err != nil {
		t.Fatalf("start secret watcher: %v", err)
	}

	atomicUpdateFile(t, mountDir, secretName, "v2", "rotated-secret")

	var rotatedCred *azidentity.ClientSecretCredential
	waitForCondition(t, 2*time.Second, 50*time.Millisecond, func() bool {
		cred, err := provider.GetCredential(tenant)
		if err != nil {
			return false
		}
		rotatedCred = cred
		return rotatedCred != initialCred
	}, "credential cache was not invalidated after secret rotation")
}
