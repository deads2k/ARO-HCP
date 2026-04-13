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

package credentials

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const (
	secretsStorePath           = "/mnt/secrets-store"
	secretsStoreEnvVar         = "SECRETS_STORE_PATH"
	defaultSecretWatchInterval = time.Minute
)

// Provider caches ClientSecretCredential instances per tenant.
type Provider struct {
	logger        *slog.Logger
	credCache     map[string]*azidentity.ClientSecretCredential
	credMu        sync.RWMutex
	watchInterval time.Duration
	watchStarted  bool
}

func NewProvider(logger *slog.Logger) *Provider {
	return &Provider{
		logger:        logger,
		credCache:     make(map[string]*azidentity.ClientSecretCredential),
		watchInterval: defaultSecretWatchInterval,
	}
}

// ValidateCredentials attempts to create credentials for all configured
// tenants. Returns an error if any secret is missing or any credential
// cannot be constructed; intended to be called at startup to fail fast.
func (p *Provider) ValidateCredentials(tenants []config.TenantConfig) error {
	for _, t := range tenants {
		if _, err := p.GetCredential(t); err != nil {
			return fmt.Errorf("tenant %s: %w", t.GetDisplayName(), err)
		}
		p.logger.Info("Validated credentials", "tenant", t.GetDisplayName())
	}
	return nil
}

func (p *Provider) GetCredential(tenant config.TenantConfig) (*azidentity.ClientSecretCredential, error) {
	key := credentialCacheKey(tenant)

	p.credMu.RLock()
	if cred, ok := p.credCache[key]; ok {
		p.credMu.RUnlock()
		return cred, nil
	}
	p.credMu.RUnlock()

	p.credMu.Lock()
	defer p.credMu.Unlock()

	if cred, ok := p.credCache[key]; ok {
		return cred, nil
	}

	secret, err := readSecret(tenant.KeyVaultSecretName)
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(
		tenant.TenantID,
		tenant.ServicePrincipalClientId,
		secret,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}

	p.credCache[key] = cred
	p.logger.Debug("Created credential for tenant", "tenant", tenant.GetDisplayName())
	return cred, nil
}

// StartWatching monitors the mounted secret files and invalidates cached
// credentials when the files change so the next GetCredential call reloads the
// rotated secret from disk.
func (p *Provider) StartWatching(ctx context.Context, tenants []config.TenantConfig) error {
	if p.watchStarted {
		return nil
	}

	type watchTarget struct {
		secretName string
		cacheKeys  []string
	}

	targets := make(map[string]*watchTarget)
	for _, tenant := range tenants {
		path := secretPath(tenant.KeyVaultSecretName)
		target, ok := targets[path]
		if !ok {
			target = &watchTarget{secretName: tenant.KeyVaultSecretName}
			targets[path] = target
		}
		target.cacheKeys = append(target.cacheKeys, credentialCacheKey(tenant))
	}

	watchCtx := utils.ContextWithLogger(ctx, utils.DefaultLogger())
	for path, target := range targets {
		secretName := target.secretName
		cacheKeys := append([]string(nil), target.cacheKeys...)

		watcher, err := utils.NewFSWatcher(path, p.watchInterval, func(_ context.Context) error {
			p.invalidateCredentials(secretName, cacheKeys)
			return nil
		})
		if err != nil {
			return fmt.Errorf("create secret watcher for %s: %w", secretName, err)
		}
		if err := watcher.Start(watchCtx); err != nil {
			return fmt.Errorf("start secret watcher for %s: %w", secretName, err)
		}

		p.logger.Info("Started secret watcher",
			"secret", secretName,
			"path", path,
			"watchInterval", p.watchInterval,
			"credentialCount", len(cacheKeys))
	}

	p.watchStarted = true
	return nil
}

func (p *Provider) invalidateCredentials(secretName string, cacheKeys []string) {
	p.credMu.Lock()
	invalidated := 0
	for _, cacheKey := range cacheKeys {
		if _, ok := p.credCache[cacheKey]; ok {
			delete(p.credCache, cacheKey)
			invalidated++
		}
	}
	p.credMu.Unlock()

	if invalidated == 0 {
		p.logger.Debug("Detected secret rotation but no cached credentials needed invalidation",
			"secret", secretName)
		return
	}

	p.logger.Info("Invalidated cached credentials after secret rotation",
		"secret", secretName,
		"credentialCount", invalidated)
}

func readSecret(secretName string) (string, error) {
	path := secretPath(secretName)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return strings.TrimSpace(string(data)), nil
}

func secretPath(secretName string) string {
	basePath := os.Getenv(secretsStoreEnvVar)
	if basePath == "" {
		basePath = secretsStorePath
	}
	return basePath + "/" + secretName
}

func credentialCacheKey(tenant config.TenantConfig) string {
	return tenant.TenantID + ":" + tenant.ServicePrincipalClientId
}
