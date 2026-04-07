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
	"reflect"
	"testing"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
)

func TestNewRoleAssignmentSource(t *testing.T) {
	type testCase struct {
		name    string
		tenants []config.TenantConfig
		want    map[string]int
	}

	testCases := []testCase{
		{
			name:    "no subscriptions yields empty limits map",
			tenants: []config.TenantConfig{{TenantID: "tenant-a"}},
			want:    map[string]int{},
		},
		{
			name: "uses default role assignment limit",
			tenants: []config.TenantConfig{
				{
					TenantID: "tenant-a",
					Subscriptions: []config.SubscriptionConfig{
						{Name: "sub-a", SubscriptionID: "sub-a-id"},
					},
				},
			},
			want: map[string]int{
				"sub-a-id": config.DefaultRoleAssignmentLimit,
			},
		},
		{
			name: "uses explicit role assignment limits",
			tenants: []config.TenantConfig{
				{
					TenantID: "tenant-a",
					Subscriptions: []config.SubscriptionConfig{
						{Name: "sub-a", SubscriptionID: "sub-a-id", RoleAssignmentLimit: 1234},
						{Name: "sub-b", SubscriptionID: "sub-b-id", RoleAssignmentLimit: 5678},
					},
				},
			},
			want: map[string]int{
				"sub-a-id": 1234,
				"sub-b-id": 5678,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source := NewRoleAssignmentSource(tc.tenants)

			if got := source.Name(); got != "rbac" {
				t.Fatalf("Name() = %q, want %q", got, "rbac")
			}
			if source.IsRegional() {
				t.Fatal("IsRegional() = true, want false")
			}
			if !reflect.DeepEqual(source.limits, tc.want) {
				t.Fatalf("limits = %#v, want %#v", source.limits, tc.want)
			}
		})
	}
}
