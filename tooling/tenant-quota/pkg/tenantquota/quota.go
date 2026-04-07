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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
)

const (
	graphAPIEndpoint = "https://graph.microsoft.com/v1.0/organization"
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

type QuotaClient struct {
	httpClient   *http.Client
	logger       *slog.Logger
	credProvider *credentials.Provider
}

func NewQuotaClient(timeout time.Duration, logger *slog.Logger, credProvider *credentials.Provider) *QuotaClient {
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
