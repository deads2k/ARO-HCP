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
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func strPtr(s string) *string { return &s }
func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestBuildComputeQuotaResult_Success(t *testing.T) {
	result, ok, err := buildComputeQuotaResult("sub-1", "westus3", &armcompute.Usage{
		CurrentValue: int32Ptr(12),
		Limit:        int64Ptr(24),
		Name: &armcompute.UsageName{
			Value:          strPtr("standardDSv3Family"),
			LocalizedValue: strPtr("Standard DSv3 Family"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected result to be emitted")
	}
	if result.QuotaName != "standardDSv3Family" {
		t.Fatalf("quota name = %q, want %q", result.QuotaName, "standardDSv3Family")
	}
	if result.LocalizedName != "Standard DSv3 Family" {
		t.Fatalf("localized name = %q, want %q", result.LocalizedName, "Standard DSv3 Family")
	}
	if result.CurrentValue != 12 {
		t.Fatalf("current value = %v, want 12", result.CurrentValue)
	}
	if result.Limit != 24 {
		t.Fatalf("limit = %v, want 24", result.Limit)
	}
}

func TestBuildNetworkQuotaResult_SkipsZeroUsage(t *testing.T) {
	result, ok, err := buildNetworkQuotaResult("sub-1", "westus3", &armnetwork.Usage{
		CurrentValue: int64Ptr(0),
		Limit:        int64Ptr(24),
		Name: &armnetwork.UsageName{
			Value:          strPtr("quota"),
			LocalizedValue: strPtr("Quota"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected zero-usage result to be skipped, got %+v", result)
	}
}

func TestBuildComputeQuotaResult_ValidatesRequiredFields(t *testing.T) {
	cases := []struct {
		name       string
		usage      *armcompute.Usage
		wantErrSub string
	}{
		{
			name: "missing currentValue",
			usage: &armcompute.Usage{
				Limit: int64Ptr(10),
				Name: &armcompute.UsageName{
					Value:          strPtr("quota"),
					LocalizedValue: strPtr("Quota"),
				},
			},
			wantErrSub: "missing currentValue",
		},
		{
			name: "missing limit",
			usage: &armcompute.Usage{
				CurrentValue: int32Ptr(1),
				Name: &armcompute.UsageName{
					Value:          strPtr("quota"),
					LocalizedValue: strPtr("Quota"),
				},
			},
			wantErrSub: "missing limit",
		},
		{
			name: "missing usage name",
			usage: &armcompute.Usage{
				CurrentValue: int32Ptr(1),
				Limit:        int64Ptr(10),
			},
			wantErrSub: "missing usage name",
		},
		{
			name: "missing quota name",
			usage: &armcompute.Usage{
				CurrentValue: int32Ptr(1),
				Limit:        int64Ptr(10),
				Name:         &armcompute.UsageName{LocalizedValue: strPtr("Quota")},
			},
			wantErrSub: "missing quota name",
		},
		{
			name: "missing localized name",
			usage: &armcompute.Usage{
				CurrentValue: int32Ptr(1),
				Limit:        int64Ptr(10),
				Name:         &armcompute.UsageName{Value: strPtr("quota")},
			},
			wantErrSub: "missing localized quota name",
		},
		{
			name:       "missing usage item",
			usage:      nil,
			wantErrSub: "missing usage item",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, err := buildComputeQuotaResult("sub-1", "eastus", tc.usage)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if ok {
				t.Fatal("expected invalid result not to be emitted")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}
