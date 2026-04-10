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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

type usageInteger interface {
	~int32 | ~int64
}

func buildComputeQuotaResult(subscriptionID, region string, usage *armcompute.Usage) (QuotaResult, bool, error) {
	if usage == nil {
		return QuotaResult{}, false, fmt.Errorf("compute usage for %s/%s missing usage item",
			subscriptionID, region)
	}
	return buildRegionalQuotaResult("compute", subscriptionID, region,
		usage.Name,
		func(name *armcompute.UsageName) *string { return name.Value },
		func(name *armcompute.UsageName) *string { return name.LocalizedValue },
		usage.CurrentValue, usage.Limit)
}

func buildNetworkQuotaResult(subscriptionID, region string, usage *armnetwork.Usage) (QuotaResult, bool, error) {
	if usage == nil {
		return QuotaResult{}, false, fmt.Errorf("network usage for %s/%s missing usage item",
			subscriptionID, region)
	}
	return buildRegionalQuotaResult("network", subscriptionID, region,
		usage.Name,
		func(name *armnetwork.UsageName) *string { return name.Value },
		func(name *armnetwork.UsageName) *string { return name.LocalizedValue },
		usage.CurrentValue, usage.Limit)
}

// buildRegionalQuotaResult validates the required Azure usage fields before
// converting them into a QuotaResult. Azure marks these fields as required, but
// we validate explicitly to avoid panicking on malformed or partial responses.
func buildRegionalQuotaResult[T usageInteger, N any](source, subscriptionID, region string,
	name *N, quotaName func(*N) *string, localizedName func(*N) *string,
	currentValue *T, limit *int64) (QuotaResult, bool, error) {

	if currentValue == nil {
		return QuotaResult{}, false, fmt.Errorf("%s usage for %s/%s missing currentValue",
			source, subscriptionID, region)
	}
	if *currentValue <= 0 {
		return QuotaResult{}, false, nil
	}
	if limit == nil {
		return QuotaResult{}, false, fmt.Errorf("%s usage for %s/%s missing limit",
			source, subscriptionID, region)
	}
	if name == nil {
		return QuotaResult{}, false, fmt.Errorf("%s usage for %s/%s missing usage name",
			source, subscriptionID, region)
	}
	value := quotaName(name)
	if value == nil || *value == "" {
		return QuotaResult{}, false, fmt.Errorf("%s usage for %s/%s missing quota name",
			source, subscriptionID, region)
	}
	localized := localizedName(name)
	if localized == nil || *localized == "" {
		return QuotaResult{}, false, fmt.Errorf("%s usage for %s/%s missing localized quota name for %q",
			source, subscriptionID, region, *value)
	}

	return QuotaResult{
		QuotaName:      *value,
		LocalizedName:  *localized,
		CurrentValue:   float64(*currentValue),
		Limit:          float64(*limit),
		SubscriptionID: subscriptionID,
		Region:         region,
	}, true, nil
}
