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

package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestBillingDumpController_SyncOnce(t *testing.T) {
	ctx := context.Background()

	clusterResourceID, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1")
	require.NoError(t, err)

	mockDB := databasetesting.NewMockDBClient()

	controller := NewBillingDumpController(mockDB)

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    clusterResourceID.SubscriptionID,
		ResourceGroupName: clusterResourceID.ResourceGroupName,
		HCPClusterName:    clusterResourceID.Name,
	}

	// SyncOnce should never return an error (best effort)
	err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)
}

func TestBillingDumpController_CooldownChecker(t *testing.T) {
	mockDB := databasetesting.NewMockDBClient()

	controller := NewBillingDumpController(mockDB)

	// Should return NoCooldown
	cooldown := controller.CooldownChecker()
	require.NotNil(t, cooldown)
}

func TestNewBillingDumpController(t *testing.T) {
	mockDB := databasetesting.NewMockDBClient()

	controller := NewBillingDumpController(mockDB)

	require.NotNil(t, controller)
}
