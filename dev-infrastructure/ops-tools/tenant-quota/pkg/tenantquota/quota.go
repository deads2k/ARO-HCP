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
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	graphAPIEndpoint           = "https://graph.microsoft.com/v1.0/organization"
	secretsStorePath           = "/mnt/secrets-store"
	secretsStoreEnvVar         = "SECRETS_STORE_PATH"
	defaultSecretWatchInterval = time.Minute
)

type QuotaData struct {
	TenantID          string
	TenantName        string
	UsagePercentage   int
	QuotaTotal        int
	QuotaUsed         int
	RemainingCapacity int
	Timestamp         time.Time
}

// CredentialProvider caches ClientSecretCredential instances per tenant.
// It is shared between the directory quota collector and the subscription
// quota collector so that both reuse the same credentials.
type CredentialProvider struct {
	logger        *slog.Logger
	credCache     map[string]*azidentity.ClientSecretCredential
	credMu        sync.RWMutex
	watchInterval time.Duration
	watchStarted  bool
}

func NewCredentialProvider(logger *slog.Logger) *CredentialProvider {
	return &CredentialProvider{
		logger:        logger,
		credCache:     make(map[string]*azidentity.ClientSecretCredential),
		watchInterval: defaultSecretWatchInterval,
	}
}

// ValidateCredentials attempts to create credentials for all configured
// tenants. Returns an error if any secret is missing or any credential
// cannot be constructed — intended to be called at startup to fail fast.
func (p *CredentialProvider) ValidateCredentials(tenants []config.TenantConfig) error {
	for _, t := range tenants {
		if _, err := p.GetCredential(t); err != nil {
			return fmt.Errorf("tenant %s: %w", t.GetDisplayName(), err)
		}
		p.logger.Info("Validated credentials", "tenant", t.GetDisplayName())
	}
	return nil
}

func (p *CredentialProvider) GetCredential(tenant config.TenantConfig) (*azidentity.ClientSecretCredential, error) {
	cacheKey := credentialCacheKey(tenant)

	p.credMu.RLock()
	if cred, ok := p.credCache[cacheKey]; ok {
		p.credMu.RUnlock()
		return cred, nil
	}
	p.credMu.RUnlock()

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

	p.credMu.Lock()
	p.credCache[cacheKey] = cred
	p.credMu.Unlock()

	p.logger.Debug("Created credential for tenant", "tenant", tenant.GetDisplayName())
	return cred, nil
}

// StartWatching monitors the mounted secret files and invalidates cached
// credentials when the files change so the next GetCredential call reloads the
// rotated secret from disk.
func (p *CredentialProvider) StartWatching(ctx context.Context, tenants []config.TenantConfig) error {
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

func (p *CredentialProvider) invalidateCredentials(secretName string, cacheKeys []string) {
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

type QuotaClient struct {
	httpClient   *http.Client
	logger       *slog.Logger
	credProvider *CredentialProvider
}

func NewQuotaClient(timeout time.Duration, logger *slog.Logger, credProvider *CredentialProvider) *QuotaClient {
	return &QuotaClient{
		httpClient:   &http.Client{Timeout: timeout},
		logger:       logger,
		credProvider: credProvider,
	}
}

func (c *QuotaClient) GetQuota(ctx context.Context, tenant config.TenantConfig) (*QuotaData, error) {
	cred, err := c.credProvider.GetCredential(tenant)
	if err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{tenant.GetScope()},
	})
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	return c.fetchQuotaFromAPI(ctx, token.Token, tenant)
}

func (c *QuotaClient) fetchQuotaFromAPI(ctx context.Context, token string, tenant config.TenantConfig) (*QuotaData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, graphAPIEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("API returned %d (failed to read body: %w)", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return parseOrganizationResponse(resp.Body, tenant)
}

type organizationResponse struct {
	Value []struct {
		DisplayName        string `json:"displayName"`
		DirectorySizeQuota struct {
			Used  *int `json:"used"`
			Total *int `json:"total"`
		} `json:"directorySizeQuota"`
	} `json:"value"`
}

func parseOrganizationResponse(body io.Reader, tenant config.TenantConfig) (*QuotaData, error) {
	var resp organizationResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(resp.Value) == 0 {
		return nil, fmt.Errorf("no organization data in response")
	}

	org := resp.Value[0]
	if org.DirectorySizeQuota.Total == nil || org.DirectorySizeQuota.Used == nil {
		return nil, fmt.Errorf("incomplete quota data")
	}

	total := *org.DirectorySizeQuota.Total
	used := *org.DirectorySizeQuota.Used

	if total <= 0 {
		return nil, fmt.Errorf("invalid quota total: %d", total)
	}

	name := tenant.TenantName
	if name == "" {
		name = org.DisplayName
	}

	return &QuotaData{
		TenantID:          tenant.TenantID,
		TenantName:        name,
		UsagePercentage:   (used * 100) / total,
		QuotaTotal:        total,
		QuotaUsed:         used,
		RemainingCapacity: total - used,
		Timestamp:         time.Now().UTC(),
	}, nil
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
