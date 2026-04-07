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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

// RoleAssignmentSource counts role assignments per subscription using the ARM
// Authorization API. This matches the count from `az role assignment list --all`
// and reflects the actual number that counts against the subscription limit.
// The limit itself is not discoverable from Azure and must be provided via
// configuration.
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

func (s *RoleAssignmentSource) Name() string     { return "rbac" }
func (s *RoleAssignmentSource) IsRegional() bool { return false }

func (s *RoleAssignmentSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
	subscriptionID string, _ string) ([]QuotaResult, []error) {

	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, []error{fmt.Errorf("create role assignments client: %w", err)}
	}

	var count int64
	pager := client.NewListForSubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, []error{fmt.Errorf("list role assignments: %w", err)}
		}
		count += int64(len(page.Value))
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
