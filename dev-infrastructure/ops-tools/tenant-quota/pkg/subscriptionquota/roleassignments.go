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
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
)

// RoleAssignmentSource counts role assignments per subscription using Azure
// Resource Graph. The limit is not discoverable from Azure and must be
// provided via configuration.
type RoleAssignmentSource struct {
	limits map[string]int // subscriptionID -> configured limit
}

func NewRoleAssignmentSource(tenants []config.TenantConfig) *RoleAssignmentSource {
	limits := make(map[string]int)
	for _, t := range tenants {
		for _, s := range t.Subscriptions {
			limits[s.SubscriptionID] = s.GetRoleAssignmentLimit()
		}
	}
	return &RoleAssignmentSource{limits: limits}
}

func (s *RoleAssignmentSource) Name() string    { return "rbac" }
func (s *RoleAssignmentSource) IsRegional() bool { return false }

func (s *RoleAssignmentSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
	subscriptionID string, _ string) ([]QuotaResult, error) {

	client, err := armresourcegraph.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create resource graph client: %w", err)
	}

	query := "authorizationresources | where type == 'microsoft.authorization/roleassignments' | summarize count()"
	subs := []*string{&subscriptionID}
	resp, err := client.Resources(ctx, armresourcegraph.QueryRequest{
		Query:         &query,
		Subscriptions: subs,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("query resource graph: %w", err)
	}

	count, err := extractCount(resp)
	if err != nil {
		return nil, fmt.Errorf("parse resource graph response: %w", err)
	}

	limit := s.limits[subscriptionID]
	return []QuotaResult{{
		QuotaName:      "roleAssignments",
		LocalizedName:  "Role Assignments",
		CurrentValue:   float64(count),
		Limit:          float64(limit),
		SubscriptionID: subscriptionID,
		Region:         "",
	}}, nil
}

func extractCount(resp armresourcegraph.ClientResourcesResponse) (int64, error) {
	data, ok := resp.Data.([]any)
	if !ok || len(data) == 0 {
		return 0, fmt.Errorf("unexpected response format: expected non-empty array")
	}

	row, ok := data[0].(map[string]any)
	if !ok {
		b, _ := json.Marshal(data[0])
		return 0, fmt.Errorf("unexpected row format: %s", string(b))
	}

	countVal, ok := row["count_"]
	if !ok {
		return 0, fmt.Errorf("missing count_ field in response row")
	}

	switch v := countVal.(type) {
	case float64:
		return int64(v), nil
	case json.Number:
		return v.Int64()
	default:
		return 0, fmt.Errorf("unexpected count_ type: %T", countVal)
	}
}
