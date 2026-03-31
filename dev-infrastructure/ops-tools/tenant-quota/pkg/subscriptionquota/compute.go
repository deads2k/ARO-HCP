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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

// ComputeQuotaSource collects compute resource quotas (vCPUs per VM family,
// total regional vCPUs, etc.) using the ARM compute usage API.
// Only quotas with currentValue > 0 are emitted (auto-discovery).
type ComputeQuotaSource struct{}

func (s *ComputeQuotaSource) Name() string    { return "compute" }
func (s *ComputeQuotaSource) IsRegional() bool { return true }

func (s *ComputeQuotaSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
	subscriptionID string, region string) ([]QuotaResult, error) {

	client, err := armcompute.NewUsageClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create compute usage client: %w", err)
	}

	var results []QuotaResult
	pager := client.NewListPager(region, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list compute usage for %s/%s: %w", subscriptionID, region, err)
		}
		for _, usage := range page.Value {
			if usage.CurrentValue == nil || *usage.CurrentValue == 0 {
				continue
			}
			var limit float64
			if usage.Limit != nil {
				limit = float64(*usage.Limit)
			}
			results = append(results, QuotaResult{
				QuotaName:      *usage.Name.Value,
				LocalizedName:  *usage.Name.LocalizedValue,
				CurrentValue:   float64(*usage.CurrentValue),
				Limit:          limit,
				SubscriptionID: subscriptionID,
				Region:         region,
			})
		}
	}
	return results, nil
}
