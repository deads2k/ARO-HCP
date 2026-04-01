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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// QuotaResult represents a single quota measurement from any Azure quota source.
type QuotaResult struct {
	QuotaName      string
	LocalizedName  string
	CurrentValue   float64
	Limit          float64
	SubscriptionID string
	Region         string // empty for non-regional quotas (e.g. role assignments)
}

// QuotaSource collects quota data from a specific Azure API.
// Implementations exist for compute, network, and role assignment quotas.
// Adding a new quota type requires only implementing this interface and
// registering the source in the collector.
type QuotaSource interface {
	// Name returns the source identifier used as the "source" metric label.
	Name() string

	// IsRegional returns true if this source collects per-region data.
	// Non-regional sources (e.g. role assignments) are called once per
	// subscription with an empty region string.
	IsRegional() bool

	// Collect gathers quota data for a single subscription and region.
	// Implementations may return both results and errors for partial success.
	Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
		subscriptionID string, region string) ([]QuotaResult, []error)
}
