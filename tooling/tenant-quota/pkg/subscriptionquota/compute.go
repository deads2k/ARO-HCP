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

func (s *ComputeQuotaSource) Name() string     { return "compute" }
func (s *ComputeQuotaSource) IsRegional() bool { return true }

func (s *ComputeQuotaSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
	subscriptionID string, region string) ([]QuotaResult, []error) {

	client, err := armcompute.NewUsageClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, []error{fmt.Errorf("create compute usage client: %w", err)}
	}

	var results []QuotaResult
	var errs []error
	pager := client.NewListPager(region, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("list compute usage for %s/%s: %w", subscriptionID, region, err))
			break
		}
		for i, usage := range page.Value {
			result, ok, err := buildComputeQuotaResult(subscriptionID, region, usage)
			if err != nil {
				errs = append(errs, fmt.Errorf("invalid compute usage item %d: %w", i, err))
				continue
			}
			if !ok {
				continue
			}
			results = append(results, result)
		}
	}
	return results, errs
}
