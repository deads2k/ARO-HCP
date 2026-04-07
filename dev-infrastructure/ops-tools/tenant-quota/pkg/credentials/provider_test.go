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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
)

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
