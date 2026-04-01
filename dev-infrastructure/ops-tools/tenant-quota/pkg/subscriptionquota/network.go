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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// NetworkQuotaSource collects network resource quotas (Public IPs, Load
// Balancers, VNets, NSGs, etc.) using the ARM network usage API.
// Only quotas with currentValue > 0 are emitted (auto-discovery).
type NetworkQuotaSource struct{}

func (s *NetworkQuotaSource) Name() string     { return "network" }
func (s *NetworkQuotaSource) IsRegional() bool { return true }

func (s *NetworkQuotaSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
	subscriptionID string, region string) ([]QuotaResult, []error) {

	client, err := armnetwork.NewUsagesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, []error{fmt.Errorf("create network usage client: %w", err)}
	}

	var results []QuotaResult
	var errs []error
	pager := client.NewListPager(region, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("list network usage for %s/%s: %w", subscriptionID, region, err))
			break
		}
		for i, usage := range page.Value {
			result, ok, err := buildNetworkQuotaResult(subscriptionID, region, usage)
			if err != nil {
				errs = append(errs, fmt.Errorf("invalid network usage item %d: %w", i, err))
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
