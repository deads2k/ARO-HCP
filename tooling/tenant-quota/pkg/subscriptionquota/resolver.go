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
	"log/slog"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
)

// ResolveSubscriptionIDs resolves subscription display names to Azure
// subscription IDs for all configured tenants. Each tenant's service
// principal credential is used to list accessible subscriptions, then
// each configured subscription name is matched to an ID.
//
// This must be called at startup before collection begins.
func ResolveSubscriptionIDs(ctx context.Context, cfg *config.Config,
	credProvider *credentials.Provider, logger *slog.Logger) error {

	for i := range cfg.Tenants {
		tenant := &cfg.Tenants[i]
		if len(tenant.Subscriptions) == 0 {
			continue
		}

		cred, err := credProvider.GetCredential(*tenant)
		if err != nil {
			return fmt.Errorf("tenant %s: get credential: %w", tenant.GetDisplayName(), err)
		}

		nameToID, err := listSubscriptions(ctx, cred)
		if err != nil {
			return fmt.Errorf("tenant %s: list subscriptions: %w", tenant.GetDisplayName(), err)
		}

		for j := range tenant.Subscriptions {
			sub := &tenant.Subscriptions[j]
			id, ok := nameToID[sub.Name]
			if !ok {
				available := make([]string, 0, len(nameToID))
				for name := range nameToID {
					available = append(available, name)
				}
				return fmt.Errorf("tenant %s: subscription %q not found; "+
					"the SP can see %d subscriptions: %v — "+
					"check that the name matches exactly and the SP has Reader role on it",
					tenant.GetDisplayName(), sub.Name, len(available), available)
			}
			sub.SubscriptionID = id
			logger.Info("Resolved subscription ID",
				"tenant", tenant.GetDisplayName(),
				"subscription", sub.Name,
				"subscriptionId", id)
		}
	}
	return nil
}

func listSubscriptions(ctx context.Context, cred *azidentity.ClientSecretCredential) (map[string]string, error) {
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create subscriptions client: %w", err)
	}

	nameToID := make(map[string]string)
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		for _, sub := range page.Value {
			if sub.DisplayName != nil && sub.SubscriptionID != nil {
				if existing, dup := nameToID[*sub.DisplayName]; dup {
					return nil, fmt.Errorf("ambiguous subscription name %q: matches both %s and %s",
						*sub.DisplayName, existing, *sub.SubscriptionID)
				}
				nameToID[*sub.DisplayName] = *sub.SubscriptionID
			}
		}
	}
	return nameToID, nil
}
